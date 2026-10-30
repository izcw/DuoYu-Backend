package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type userProfile struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Server) userInfo(ctx context.Context, token string) (userProfile, error) {
	var info userProfile
	var discovery struct {
		UserInfo string `json:"userinfo_endpoint"`
	}
	s.provider.Claims(&discovery)
	if !strings.HasPrefix(discovery.UserInfo, s.cfg.Issuer+"/") {
		return info, fail(503, "账号服务配置错误")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", discovery.UserInfo, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, e := s.http.Do(req)
	if e != nil {
		return info, fail(503, "账号服务暂不可用")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode >= 500 || res.StatusCode == 429 {
			return info, fail(503, "账号服务暂不可用")
		}
		return info, fail(401, "登录已失效，请重新登录")
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&info) != nil || info.Sub == "" {
		return info, fail(401, "无效账号身份")
	}
	return info, nil
}

type pendingLogin struct {
	Verifier, Nonce string
	Expires         time.Time
}
type adminSession struct {
	Token oauth2.Token `json:"token"`
	Sub   string       `json:"sub"`
	CSRF  string       `json:"csrf"`
}

func (s *Server) oauth() oauth2.Config {
	endpoint := s.provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return oauth2.Config{ClientID: s.cfg.AdminClientID, Endpoint: endpoint, RedirectURL: s.cfg.Origin + "/auth/callback", Scopes: []string{"openid", "profile", "offline_access"}}
}
func (s *Server) seal(v any) ([]byte, error) {
	block, e := aes.NewCipher(s.cfg.Key)
	if e != nil {
		return nil, e
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return gcm.Seal(nonce, nonce, b, []byte("duoyu-admin-session-v1")), nil
}
func (s *Server) unseal(b []byte, v any) error {
	block, e := aes.NewCipher(s.cfg.Key)
	if e != nil {
		return e
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	if len(b) < gcm.NonceSize() {
		return fail(401, "登录已失效")
	}
	data, e := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte("duoyu-admin-session-v1"))
	if e != nil {
		return e
	}
	return json.Unmarshal(data, v)
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, max int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: strings.HasPrefix(s.cfg.Origin, "https:"), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: max})
}
func (s *Server) authRoute(w http.ResponseWriter, r *http.Request) error {
	switch r.URL.Path {
	case "/auth/login":
		if r.Method != "GET" {
			return fail(405, "方法不支持")
		}
		state, verifier, nonce := randomToken(), oauth2.GenerateVerifier(), randomToken()
		s.pending.Range(func(k, v any) bool {
			if v.(pendingLogin).Expires.Before(time.Now()) {
				s.pending.Delete(k)
			}
			return true
		})
		count := 0
		s.pending.Range(func(k, v any) bool { count++; return true })
		if count > 1000 {
			return fail(429, "请求过于频繁")
		}
		s.pending.Store(digest(state), pendingLogin{verifier, nonce, time.Now().Add(10 * time.Minute)})
		s.cookie(w, "duoyu_admin_state", state, 600)
		cfg := s.oauth()
		http.Redirect(w, r, cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)), 302)
		return nil
	case "/auth/callback":
		if r.Method != "GET" {
			return fail(405, "方法不支持")
		}
		state := r.URL.Query().Get("state")
		cookie, e := r.Cookie("duoyu_admin_state")
		if e != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
			return fail(400, "登录校验失败，请重新登录")
		}
		v, ok := s.pending.LoadAndDelete(digest(state))
		s.cookie(w, "duoyu_admin_state", "", -1)
		if !ok || v.(pendingLogin).Expires.Before(time.Now()) {
			return fail(400, "登录请求已过期")
		}
		p := v.(pendingLogin)
		cfg := s.oauth()
		ctx := context.WithValue(r.Context(), oauth2.HTTPClient, s.http)
		token, e := cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(p.Verifier))
		if e != nil {
			return fail(401, "登录未完成，请重试")
		}
		raw, _ := token.Extra("id_token").(string)
		id, e := s.provider.Verifier(&oidc.Config{ClientID: s.cfg.AdminClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(r.Context(), raw)
		if e != nil || id.Nonce != p.Nonce {
			return fail(401, "登录身份校验失败")
		}
		info, e := s.userInfo(r.Context(), token.AccessToken)
		if e != nil {
			return e
		}
		if info.Sub != id.Subject || info.Sub != s.cfg.AdminSub {
			return fail(403, "仅账号 8888888888 可进入管理系统")
		}
		session := adminSession{*token, info.Sub, randomToken()}
		encrypted, e := s.seal(session)
		if e != nil {
			return e
		}
		key := randomToken()
		_, e = s.db.Exec(r.Context(), "INSERT INTO backend_sessions(hash,encrypted,expires_at) VALUES($1,$2,now()+interval '12 hours')", digest(key), encrypted)
		if e != nil {
			return e
		}
		s.cookie(w, "duoyu_admin", key, 43200)
		s.audit(r.Context(), "admin.login", "")
		http.Redirect(w, r, "/admin/", 302)
		return nil
	case "/auth/logout":
		if r.Method != "POST" {
			return fail(405, "方法不支持")
		}
		if e := s.requireAdmin(w, r); e != nil {
			return e
		}
		if c, e := r.Cookie("duoyu_admin"); e == nil {
			s.db.Exec(r.Context(), "DELETE FROM backend_sessions WHERE hash=$1", digest(c.Value))
		}
		s.cookie(w, "duoyu_admin", "", -1)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	return fail(404, "接口不存在")
}

type csrfContext struct{}
type profileContext struct{}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) error {
	c, e := r.Cookie("duoyu_admin")
	if e != nil {
		return fail(401, "请使用多鱼账号登录")
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	var b []byte
	e = tx.QueryRow(r.Context(), "SELECT encrypted FROM backend_sessions WHERE hash=$1 AND expires_at>now() FOR UPDATE", digest(c.Value)).Scan(&b)
	if e != nil {
		return fail(401, "管理会话已过期")
	}
	var sess adminSession
	if s.unseal(b, &sess) != nil || sess.Sub != s.cfg.AdminSub {
		return fail(403, "需要唯一超级管理员权限")
	}
	if !sess.Token.Valid() {
		cfg := s.oauth()
		ctx := context.WithValue(r.Context(), oauth2.HTTPClient, s.http)
		token, e := cfg.TokenSource(ctx, &sess.Token).Token()
		if e != nil {
			return fail(401, "登录已失效，请重新登录")
		}
		sess.Token = *token
		encrypted, e := s.seal(sess)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(r.Context(), "UPDATE backend_sessions SET encrypted=$2 WHERE hash=$1", digest(c.Value), encrypted); e != nil {
			return e
		}
	}
	info, e := s.userInfo(r.Context(), sess.Token.AccessToken)
	if e != nil {
		return e
	}
	if info.Sub != sess.Sub {
		return fail(403, "身份已改变")
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if r.Header.Get("Origin") != s.cfg.Origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRF)) != 1 {
			return fail(403, "请求校验失败，请刷新页面")
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	ctx := context.WithValue(r.Context(), csrfContext{}, sess.CSRF)
	ctx = context.WithValue(ctx, profileContext{}, info)
	*r = *r.WithContext(ctx)
	return nil
}
