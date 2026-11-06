package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAdminRefreshTemporaryFailure(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	session := adminSession{Token: oauth2.Token{AccessToken: "expired", RefreshToken: "refresh-test", Expiry: time.Now().Add(-time.Hour)}, Sub: "trusted-super", CSRF: "csrf-test"}
	encrypted, err := s.seal(session)
	if err != nil {
		t.Fatal(err)
	}
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	for _, tc := range []struct {
		name         string
		status, want int
	}{
		{"rate limited", 429, 503}, {"server error", 500, 503}, {"unavailable", 503, 503},
		{"transport failure", 0, 503}, {"invalid grant", 400, 401}, {"denied", 401, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(context.Background(), "UPDATE backend_sessions SET encrypted=$2 WHERE hash=$1", digest(cookie.Value), encrypted); err != nil {
				t.Fatal(err)
			}
			s.http.Transport = siteRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/token" {
					return base.RoundTrip(r)
				}
				if tc.status == 0 {
					return nil, errors.New("isolated connection interrupted")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)), Request: r}, nil
			})
			response := callAdmin(s, cookie, "GET", "me", "", false)
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
			var retained []byte
			if err := s.db.QueryRow(context.Background(), "SELECT encrypted FROM backend_sessions WHERE hash=$1", digest(cookie.Value)).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(retained, encrypted) {
				t.Fatal("failed refresh changed durable credentials")
			}
			if tc.want == 503 {
				s.http.Transport = siteRoundTrip(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path != "/token" {
						return base.RoundTrip(r)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"admin","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)), Request: r}, nil
				})
				if recovered := callAdmin(s, cookie, "GET", "me", "", false); recovered.Code != 200 {
					t.Fatalf("same session did not recover: %d %s", recovered.Code, recovered.Body.String())
				}
			}
		})
	}
}

func TestAdminCSRFFailureDoesNotRotateCredentials(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	session := adminSession{Token: oauth2.Token{AccessToken: "expired", RefreshToken: "refresh-test", Expiry: time.Now().Add(-time.Hour)}, Sub: "trusted-super", CSRF: "csrf-test"}
	encrypted, err := s.seal(session)
	if err != nil {
		t.Fatal(err)
	}
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	for _, tc := range []struct{ name, origin, csrf string }{
		{"foreign origin", "https://other.backend.test", "csrf-test"},
		{"missing origin", "", "csrf-test"},
		{"missing csrf", s.cfg.Origin, ""},
		{"wrong csrf", s.cfg.Origin, "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(context.Background(), "UPDATE backend_sessions SET encrypted=$2 WHERE hash=$1", digest(cookie.Value), encrypted); err != nil {
				t.Fatal(err)
			}
			refreshes := 0
			s.http.Transport = siteRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/token" {
					return base.RoundTrip(r)
				}
				refreshes++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"admin","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)), Request: r}, nil
			})
			r := httptest.NewRequest("POST", s.cfg.Origin+"/api/admin/settings", strings.NewReader(`{"quota_mb":200}`))
			r.AddCookie(cookie)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.csrf)
			w := httptest.NewRecorder()
			s.handler(w, r)
			if w.Code != 403 {
				t.Fatalf("expected CSRF rejection, got %d: %s", w.Code, w.Body.String())
			}
			if refreshes != 0 {
				t.Errorf("rejected request rotated remote credentials %d times", refreshes)
			}
			var retained []byte
			if err := s.db.QueryRow(context.Background(), "SELECT encrypted FROM backend_sessions WHERE hash=$1", digest(cookie.Value)).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(retained, encrypted) {
				t.Fatal("CSRF rejection changed durable credentials")
			}
		})
	}
}

func TestAdminLogoutDeleteFailureAndRetry(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	// Only the fixture's private schema is affected. Authentication still succeeds;
	// a failing DELETE exercises the actual logout failure, not an earlier DB read.
	if _, err := s.db.Exec(context.Background(), `CREATE FUNCTION reject_logout_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated deletion failure'; END $$;
	CREATE TRIGGER reject_logout_delete BEFORE DELETE ON backend_sessions FOR EACH ROW EXECUTE FUNCTION reject_logout_delete();`); err != nil {
		t.Fatal(err)
	}
	logout := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", s.cfg.Origin+"/auth/logout", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", s.cfg.Origin)
		r.Header.Set("X-CSRF-Token", "csrf-test")
		w := httptest.NewRecorder()
		s.handler(w, r)
		return w
	}
	failed := logout()
	if failed.Code != 500 {
		t.Errorf("failed deletion reported %d instead of 500", failed.Code)
	}
	for _, c := range failed.Result().Cookies() {
		if c.Name == "duoyu_admin" && c.MaxAge < 0 {
			t.Error("failed logout discarded retry cookie while durable session remained usable")
		}
	}
	if r := callAdmin(s, cookie, "GET", "me", "", false); r.Code != 200 {
		t.Fatalf("session could not retry after failed deletion: %d", r.Code)
	}
	if _, err := s.db.Exec(context.Background(), "DROP TRIGGER reject_logout_delete ON backend_sessions"); err != nil {
		t.Fatal(err)
	}
	if r := logout(); r.Code != 200 {
		t.Fatalf("retry failed: %d %s", r.Code, r.Body.String())
	}
	if r := callAdmin(s, cookie, "GET", "me", "", false); r.Code != 401 {
		t.Fatalf("old cookie remained usable after successful logout: %d", r.Code)
	}
}

func TestAdminSessionQueryFailureIsNotExpired(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	// The fixture's private schema owns this table. No shared/production schema is modified.
	if _, err := s.db.Exec(context.Background(), "ALTER TABLE backend_sessions RENAME TO interrupted_sessions"); err != nil {
		t.Fatal(err)
	}
	response := callAdmin(s, cookie, "GET", "me", "", false)
	if response.Code != 500 {
		t.Fatalf("database failure was reported as %d: %s", response.Code, response.Body.String())
	}
	if _, err := s.db.Exec(context.Background(), "ALTER TABLE interrupted_sessions RENAME TO backend_sessions"); err != nil {
		t.Fatal(err)
	}
	if response := callAdmin(s, cookie, "GET", "me", "", false); response.Code != 200 {
		t.Fatalf("recovered session failed: %d", response.Code)
	}
}

func TestAdminRotatedCredentialsSurviveUserInfoFailure(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	sess := adminSession{Token: oauth2.Token{AccessToken: "expired", RefreshToken: "refresh-test", Expiry: time.Now().Add(-time.Hour)}, Sub: "trusted-super", CSRF: "csrf-test"}
	encrypted, err := s.seal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(context.Background(), "UPDATE backend_sessions SET encrypted=$2 WHERE hash=$1", digest(cookie.Value), encrypted); err != nil {
		t.Fatal(err)
	}
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	refreshes, failUserInfo := 0, true
	s.http.Transport = siteRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			refreshes++
			// A real provider rotates once; replaying the previous token is rejected.
			status, body := 200, `{"access_token":"admin","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`
			if refreshes > 1 {
				status, body = 400, `{"error":"invalid_grant"}`
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		if r.URL.Path == "/userinfo" && failUserInfo {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("unavailable")), Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	if r := callAdmin(s, cookie, "GET", "me", "", false); r.Code != 503 {
		t.Fatalf("expected transient UserInfo failure, got %d", r.Code)
	}
	var stored []byte
	if err = s.db.QueryRow(context.Background(), "SELECT encrypted FROM backend_sessions WHERE hash=$1", digest(cookie.Value)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var saved adminSession
	if err = s.unseal(stored, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Token.RefreshToken != "rotated" {
		t.Error("successful remote rotation was lost when UserInfo failed")
	}
	failUserInfo = false
	if r := callAdmin(s, cookie, "GET", "me", "", false); r.Code != 200 {
		t.Errorf("same session failed to recover after UserInfo recovered: %d", r.Code)
	}
	if refreshes != 1 {
		t.Errorf("retried consumed refresh credential: %d refreshes", refreshes)
	}
}

func TestAdminConcurrentRefreshRotatesOnce(t *testing.T) {
	s := testServer(t)
	cookie := loginCookie(t, s, "trusted-super", "admin")
	sess := adminSession{Token: oauth2.Token{AccessToken: "expired", RefreshToken: "refresh-test", Expiry: time.Now().Add(-time.Hour)}, Sub: "trusted-super", CSRF: "csrf-test"}
	encrypted, err := s.seal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(context.Background(), "UPDATE backend_sessions SET encrypted=$2 WHERE hash=$1", digest(cookie.Value), encrypted); err != nil {
		t.Fatal(err)
	}
	base := s.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var refreshes atomic.Int32
	s.http.Transport = siteRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/token" {
			return base.RoundTrip(r)
		}
		refreshes.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"admin","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)), Request: r}, nil
	})
	start, results := make(chan struct{}), make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() { <-start; results <- callAdmin(s, cookie, "GET", "me", "", false).Code }()
	}
	close(start)
	for i := 0; i < 8; i++ {
		if code := <-results; code != 200 {
			t.Errorf("concurrent session check failed: %d", code)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh rotation called %d times", refreshes.Load())
	}
}
