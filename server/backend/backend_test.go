package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("BACKEND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Set BACKEND_TEST_DATABASE_URL to the dedicated local test database")
	}
	if !strings.Contains(dsn, "127.0.0.1:55432/duoyu_sync_test") {
		t.Fatal("Tests require dedicated local PostgreSQL")
	}
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "backend_test_" + strings.ToLower(randomToken()[:12])
	schema = strings.ReplaceAll(strings.ReplaceAll(schema, "-", "a"), "_", "b")
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); root.Close() })
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	for _, path := range []string{"../sync/schema.sql", "schema.sql"} {
		sql, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(sql)); e != nil {
			t.Fatal(e)
		}
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			writeJSON(w, 200, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
		case "/test-issue":
			sub, aud := r.URL.Query().Get("sub"), r.URL.Query().Get("aud")
			if sub == "" {
				sub = "ordinary"
			}
			if aud == "" {
				aud = "extension"
			}
			claims, _ := json.Marshal(map[string]any{"iss": issuer.URL, "sub": sub, "aud": aud, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
			input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
			hash := sha256.Sum256([]byte(input))
			signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if e != nil {
				t.Error(e)
				return
			}
			w.Write([]byte(input + "." + base64.RawURLEncoding.EncodeToString(signature)))
		case "/.well-known/openid-configuration":
			writeJSON(w, 200, map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/jwks", "userinfo_endpoint": issuer.URL + "/userinfo", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/userinfo":
			if r.Header.Get("Authorization") == "Bearer admin" {
				writeJSON(w, 200, map[string]string{"sub": "trusted-super", "name": "测试管理员", "picture": "https://identity.test/avatar/admin.png"})
			} else if r.Header.Get("Authorization") == "Bearer ordinary" {
				writeJSON(w, 200, map[string]string{"sub": "ordinary", "name": "测试用户", "picture": "https://identity.test/avatar/user.png"})
			} else {
				w.WriteHeader(401)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(issuer.Close)
	provider, e := oidc.NewProvider(ctx, issuer.URL)
	if e != nil {
		t.Fatal(e)
	}
	return &Server{db: db, cfg: Config{Origin: "https://backend.test", Issuer: issuer.URL, ClientID: "extension", AdminClientID: "admin", AdminSub: "trusted-super", Key: bytes.Repeat([]byte{1}, 32), AssetRoot: t.TempDir(), BackupDir: t.TempDir()}, provider: provider, http: issuer.Client()}
}
func loginCookie(t *testing.T, s *Server, sub, access string) *http.Cookie {
	t.Helper()
	v := adminSession{Token: oauth2.Token{AccessToken: access, Expiry: time.Now().Add(time.Hour)}, Sub: sub, CSRF: "csrf-test"}
	b, e := s.seal(v)
	if e != nil {
		t.Fatal(e)
	}
	key := randomToken()
	_, e = s.db.Exec(context.Background(), "INSERT INTO backend_sessions VALUES($1,$2,now()+interval '1 hour')", digest(key), b)
	if e != nil {
		t.Fatal(e)
	}
	return &http.Cookie{Name: "duoyu_admin", Value: key}
}
func callAdmin(s *Server, c *http.Cookie, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://backend.test/api/admin/"+path, strings.NewReader(body))
	if c != nil {
		r.AddCookie(c)
	}
	if csrf {
		r.Header.Set("Origin", s.cfg.Origin)
		r.Header.Set("X-CSRF-Token", "csrf-test")
	}
	w := httptest.NewRecorder()
	s.handler(w, r)
	return w
}
func TestAdminBoundary(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	ordinary := loginCookie(t, s, "ordinary", "ordinary")
	for _, c := range []*http.Cookie{nil, ordinary} {
		r := callAdmin(s, c, "GET", "overview", "", false)
		if r.Code != 401 && r.Code != 403 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if r := callAdmin(s, admin, "GET", "me", "", false); r.Code != 200 || !strings.Contains(r.Body.String(), "https://identity.test/avatar/admin.png") {
		t.Fatal("admin avatar missing", r.Body.String())
	}
	if r := callAdmin(s, admin, "GET", "overview", "", false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := callAdmin(s, admin, "POST", "settings", `{"quota_mb":200}`, false); r.Code != 403 {
		t.Fatal("CSRF was accepted")
	}
	if r := callAdmin(s, admin, "POST", "settings", `{"quota_mb":200}`, true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := callAdmin(s, admin, "POST", "settings", `{"quota_mb":200} {}`, true); r.Code != 400 {
		t.Fatal("trailing input accepted")
	}
	w := httptest.NewRecorder()
	s.handler(w, httptest.NewRequest("GET", "https://backend.test/auth/callback?state=forged&code=forged", nil))
	if w.Code != 400 {
		t.Fatal("callback accepted invalid state")
	}
}

func TestSignedExtensionIdentityAndPrivateFiles(t *testing.T) {
	s := testServer(t)
	issue := func(query string) string {
		res, e := http.Get(s.cfg.Issuer + "/test-issue" + query)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(res.Body)
		return b.String()
	}
	ordinary, admin, wrongAudience := issue(""), issue("?sub=trusted-super"), issue("?aud=wrong-project")
	call := func(method, path, identity, access string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://backend.test"+path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("X-Duoyu-Identity", identity)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		s.handler(w, r)
		return w
	}
	r := call("GET", "/v1/catalog/widgets", ordinary, "ordinary", nil)
	if r.Code != 200 {
		t.Fatal("valid signed extension session failed", r.Body.String())
	}
	for _, identity := range []string{wrongAudience, admin, ordinary + "tampered"} {
		r = call("GET", "/v1/catalog/widgets", identity, "ordinary", nil)
		if r.Code != 401 {
			t.Fatal("invalid identity accepted", r.Body.String())
		}
	}
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	hash := digest(string(png))
	r = call("POST", "/v1/assets/"+hash, ordinary, "ordinary", png)
	if r.Code != 200 {
		t.Fatal("signed upload failed", r.Body.String())
	}
	r = call("GET", "/v1/assets/"+hash, ordinary, "ordinary", nil)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), png) {
		t.Fatal("signed file read failed")
	}
	r = call("GET", "/v1/assets/"+hash, admin, "admin", nil)
	if r.Code != 404 {
		t.Fatal("private file leaked across users")
	}
	r = call("GET", "/api/admin/overview", ordinary, "ordinary", nil)
	if r.Code != 401 {
		t.Fatal("extension bearer bypassed administrator cookie boundary")
	}
}
func TestCatalogVersions(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	r := callAdmin(s, admin, "POST", "catalog/widgets", `{"name":"便笺","description":"","status":"draft","payload":{"type":"note"},"version":0}`, true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var v map[string]string
	json.Unmarshal(r.Body.Bytes(), &v)
	path := "catalog/widgets/" + v["id"]
	r = callAdmin(s, admin, "POST", path, `{"name":"便笺二","description":"","status":"published","payload":{"type":"note"},"version":1}`, true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = callAdmin(s, admin, "POST", path, `{"name":"旧修改","description":"","status":"draft","payload":{},"version":1}`, true)
	if r.Code != 409 {
		t.Fatal("stale catalog update accepted")
	}
	r = callAdmin(s, admin, "GET", "charts", "", false)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
}
func TestFilesQuotaIsolationAndMigration(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	u := Identity{accountID(s.cfg.Issuer, "first"), "first", "first", ""}
	other := Identity{accountID(s.cfg.Issuer, "second"), "second", "second", ""}
	if e := s.record(ctx, u); e != nil {
		t.Fatal(e)
	}
	if e := s.record(ctx, other); e != nil {
		t.Fatal(e)
	}
	s.db.Exec(ctx, "UPDATE backend_users SET quota_bytes=12 WHERE account_id=$1", u.ID)
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	sum := sha256.Sum256(png)
	hash := hex.EncodeToString(sum[:])
	upload := func(data []byte) error {
		sum := sha256.Sum256(data)
		r := httptest.NewRequest("POST", "/v1/assets/"+hex.EncodeToString(sum[:]), bytes.NewReader(data))
		r.Header.Set("Content-Type", "image/png")
		return s.assetRoute(httptest.NewRecorder(), r, u)
	}
	if e := upload(png); e != nil {
		t.Fatal(e)
	}
	if e := upload(png); e != nil {
		t.Fatal("dedup failed", e)
	}
	if b, e := os.ReadFile(filepath.Join(s.cfg.AssetRoot, u.ID, hash)); e != nil || !bytes.Equal(b, png) {
		t.Fatal("file not persisted")
	}
	var stored []byte
	var size int64
	s.db.QueryRow(ctx, "SELECT body,byte_size FROM sync_assets WHERE account_id=$1", u.ID).Scan(&stored, &size)
	if stored != nil || size != 8 {
		t.Fatal("database retained image bytes")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := upload(append(append([]byte{}, png...), byte(i)))
			if e == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if accepted != 0 {
		t.Fatal("parallel uploads exceeded quota")
	}
	w := httptest.NewRecorder()
	if e := s.serveAsset(w, httptest.NewRequest("GET", "/", nil), other.ID, hash); e == nil {
		t.Fatal("cross account read accepted")
	}
	w = httptest.NewRecorder()
	if e := s.serveAsset(w, httptest.NewRequest("GET", "/", nil), u.ID, hash); e != nil || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatal("file fetch failed", e)
	}
	old := append(append([]byte{}, png...), 99)
	oldsum := sha256.Sum256(old)
	oldhash := hex.EncodeToString(oldsum[:])
	_, e := s.db.Exec(ctx, "INSERT INTO sync_assets(account_id,sha256,media_type,body,byte_size) VALUES($1,$2,'image/png',$3,$4)", other.ID, oldhash, old, len(old))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.migrateAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(s.cfg.AssetRoot, other.ID, oldhash)); e != nil || !bytes.Equal(b, old) {
		t.Fatal("legacy migration failed")
	}
}

func TestUserSnapshotAndOfficialDefault(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	admin := loginCookie(t, s, "trusted-super", "admin")
	source := Identity{accountID(s.cfg.Issuer, "template-source"), "template-source", "来源用户", ""}
	s.record(ctx, source)
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	hash := digest(string(png))
	s.persistFile(source.ID, hash, png)
	_, e := s.db.Exec(ctx, "INSERT INTO sync_assets(account_id,sha256,media_type,body,byte_size,file_path) VALUES($1,$2,'image/png',NULL,8,$3)", source.ID, hash, s.relativeFile(source.ID, hash))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(ctx, `INSERT INTO sync_entities(account_id,entity_type,entity_id,payload,revision) VALUES($1,'libraryItems','source-link',$2,1)`, source.ID, []byte(`{"id":"source-link","kind":"link","name":"初始链接","pageId":"home","parent":"","url":"https://example.invalid","icon":"duoyu-asset:`+hash+`","layout":{"x":0,"y":0,"w":1,"h":1}}`))
	if e != nil {
		t.Fatal(e)
	}
	r := callAdmin(s, admin, "POST", "templates/capture", fmt.Sprintf(`{"account_id":%q,"name":"官方桌面"}`, source.ID), true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var captured map[string]string
	json.Unmarshal(r.Body.Bytes(), &captured)
	template := captured["id"]
	r = callAdmin(s, admin, "POST", "catalog/templates/"+template, `{"name":"官方桌面","description":"","status":"published","payload":{},"version":1}`, true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = callAdmin(s, admin, "POST", "templates/default", fmt.Sprintf(`{"id":%q}`, template), true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	// The source is mutable; the template snapshot and its images are independent.
	s.db.Exec(ctx, "UPDATE sync_entities SET payload=jsonb_set(payload,'{name}','\"来源已修改\"') WHERE account_id=$1", source.ID)
	os.Remove(filepath.Join(s.cfg.AssetRoot, source.ID, hash))
	fresh := Identity{accountID(s.cfg.Issuer, "fresh-user"), "fresh-user", "新用户", ""}
	s.record(ctx, fresh)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.initializeAccount(ctx, fresh) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	var name, id string
	s.db.QueryRow(ctx, "SELECT count(*),min(payload->>'name'),min(entity_id) FROM sync_entities WHERE account_id=$1", fresh.ID).Scan(&count, &name, &id)
	if count != 1 || name != "初始链接" || id == "source-link" {
		t.Fatal("default snapshot incorrectly seeded", count, name, id)
	}
	if _, e = os.Stat(filepath.Join(s.cfg.AssetRoot, fresh.ID, hash)); e != nil {
		t.Fatal("template image not copied")
	}
	s.db.QueryRow(ctx, "SELECT count(*) FROM sync_changes WHERE account_id=$1", fresh.ID).Scan(&count)
	if count != 1 {
		t.Fatal("default applied more than once")
	}
	if e = s.initializeAccount(ctx, source); e != nil {
		t.Fatal(e)
	}
	s.db.QueryRow(ctx, "SELECT count(*) FROM sync_entities WHERE account_id=$1", source.ID).Scan(&count)
	if count != 1 {
		t.Fatal("existing user was overwritten")
	}
	r = callAdmin(s, admin, "DELETE", "catalog/templates/"+template+"?version=2", "", true)
	if r.Code != 409 {
		t.Fatal("active default template deleted")
	}
	request := newUUID()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"request_id":%q}`, request)))
		if e = s.applyTemplate(w, req, fresh, template); e != nil {
			t.Fatal(e)
		}
	}
	s.db.QueryRow(ctx, "SELECT count(*) FROM sync_changes WHERE account_id=$1", fresh.ID).Scan(&count)
	if count != 2 {
		t.Fatal("retry duplicated template")
	}
	// Configuration changes cannot make the official template unusable for new accounts.
	s.db.Exec(ctx, "UPDATE backend_catalog SET payload=jsonb_set(payload,'{assets,0,bytes}','2097152') WHERE id=$1", template)
	r = callAdmin(s, admin, "POST", "settings", `{"quota_mb":1}`, true)
	if r.Code != 409 {
		t.Fatal("quota reduced below official template size", r.Body.String())
	}
	s.db.Exec(ctx, "UPDATE backend_settings SET value='1' WHERE id='storage_quota_bytes'")
	r = callAdmin(s, admin, "POST", "templates/default", fmt.Sprintf(`{"id":%q}`, template), true)
	if r.Code != 409 {
		t.Fatal("oversized official template accepted", r.Body.String())
	}
}

func TestUserAvatarProfile(t *testing.T) {
	s := testServer(t)
	u := Identity{accountID(s.cfg.Issuer, "profile-user"), "profile-user", "头像用户", "https://identity.test/avatar/first.png"}
	if e := s.record(context.Background(), u); e != nil {
		t.Fatal(e)
	}
	u.Picture = "https://identity.test/avatar/changed.png"
	if e := s.record(context.Background(), u); e != nil {
		t.Fatal(e)
	}
	admin := loginCookie(t, s, "trusted-super", "admin")
	response := callAdmin(s, admin, "GET", "users", "", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), u.Picture) || strings.Contains(response.Body.String(), "first.png") {
		t.Fatal("User avatar profile did not refresh", response.Body.String())
	}
}
func TestAdminQuotaModesAndPreview(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	u := Identity{accountID(s.cfg.Issuer, "quota-preview"), "quota-preview", "预览用户", ""}
	if e := s.record(ctx, u); e != nil {
		t.Fatal(e)
	}
	admin := loginCookie(t, s, "trusted-super", "admin")
	ordinary := loginCookie(t, s, "ordinary", "ordinary")
	settings := func() map[string]any {
		r := callAdmin(s, admin, "GET", "users/"+u.ID+"/settings", "", false)
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
		var out struct {
			Items []map[string]any `json:"items"`
		}
		json.Unmarshal(r.Body.Bytes(), &out)
		return out.Items[0]
	}
	if row := settings(); row["quota_bytes"] != nil || row["effective_quota_bytes"] != "104857600" {
		t.Fatal("default missing", row)
	}
	for _, body := range []string{`{"disabled":false,"quota_mb":-1}`, `{"disabled":false,"quota_mb":1.5}`, `{"disabled":false,"quota_mb":102401}`} {
		if r := callAdmin(s, admin, "POST", "users/"+u.ID, body, true); r.Code != 400 {
			t.Fatal("invalid quota accepted", r.Code)
		}
	}
	for _, quota := range []string{"0", "200", "null"} {
		r := callAdmin(s, admin, "POST", "users/"+u.ID, `{"disabled":false,"quota_mb":`+quota+`}`, true)
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
		row := settings()
		if quota == "null" && row["quota_bytes"] != nil {
			t.Fatal(row)
		}
		if quota == "0" && row["effective_quota_bytes"] != "0" {
			t.Fatal(row)
		}
		if quota == "200" && row["effective_quota_bytes"] != "209715200" {
			t.Fatal(row)
		}
	}
	if r := callAdmin(s, admin, "POST", "settings", `{"quota_mb":50}`, true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if row := settings(); row["effective_quota_bytes"] != "52428800" || row["default_quota_bytes"] != "52428800" {
		t.Fatal("inherit did not track global", row)
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF7sAAAAASUVORK5CYII=")
	sum := sha256.Sum256(png)
	hash := hex.EncodeToString(sum[:])
	request := httptest.NewRequest("POST", "/v1/assets/"+hash, bytes.NewReader(png))
	request.Header.Set("Content-Type", "image/png")
	if e := s.assetRoute(httptest.NewRecorder(), request, u); e != nil {
		t.Fatal(e)
	}
	path := "users/" + u.ID + "/files/" + hash
	r := callAdmin(s, admin, "GET", path, "", false)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), png) || r.Header().Get("Content-Type") != "image/png" || r.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("preview failed", r.Code, r.Body.String())
	}
	for _, cookie := range []*http.Cookie{nil, ordinary} {
		if r := callAdmin(s, cookie, "GET", path, "", false); r.Code != 401 && r.Code != 403 {
			t.Fatal("unprivileged preview accepted", r.Code)
		}
	}
	if r := callAdmin(s, admin, "GET", "users/"+accountID(s.cfg.Issuer, "missing")+"/settings", "", false); r.Code != 404 {
		t.Fatal("missing user accepted", r.Code)
	}
	if row := settings(); row["storage_bytes"] != "68" {
		t.Fatal("usage not reported", row)
	}
}
