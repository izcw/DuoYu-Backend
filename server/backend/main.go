package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Origin, Issuer, ClientID, AdminClientID, AdminSub, AssetRoot, AdminDist, BackupDir string
	Key                                                                                []byte
	Origins                                                                            []string
}
type Server struct {
	db       *pgxpool.Pool
	cfg      Config
	provider *oidc.Provider
	proxy    *httputil.ReverseProxy
	pending  sync.Map
	http     *http.Client
}
type Identity struct{ ID, Sub, Name, Picture string }
type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string            { return e.message }
func fail(status int, message string) error { return apiError{status, message} }
func env(k, def string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return def
}
func digest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func accountID(issuer, sub string) string {
	h := []byte(digest(issuer + "\x00" + sub)[:32])
	h[12] = '4'
	h[16] = '8'
	s := string(h)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return fail(400, "请求格式不正确")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return fail(400, "请求包含多余内容")
	}
	return nil
}
func (s *Server) tx(ctx context.Context, id string, fn func(pgx.Tx) error) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT set_config('app.account_id',$1,true)", id); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Server) record(ctx context.Context, u Identity) error {
	return s.tx(ctx, u.ID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "INSERT INTO sync_accounts(id,issuer,subject) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", u.ID, s.cfg.Issuer, u.Sub)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, "INSERT INTO backend_users(account_id,name,picture) VALUES($1,$2,$3) ON CONFLICT(account_id) DO UPDATE SET name=excluded.name,picture=excluded.picture,last_seen=now()", u.ID, u.Name, u.Picture)
		return e
	})
}
func (s *Server) audit(ctx context.Context, action, target string) error {
	_, e := s.db.Exec(ctx, "INSERT INTO backend_audit(actor,action,target) VALUES($1,$2,$3)", s.cfg.AdminSub, action, target)
	return e
}
func (s *Server) authenticate(r *http.Request) (Identity, error) {
	var zero Identity
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	identity := r.Header.Get("X-Duoyu-Identity")
	if token == "" || token == r.Header.Get("Authorization") || identity == "" {
		return zero, fail(401, "请先登录")
	}
	id, e := s.provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(r.Context(), identity)
	if e != nil {
		return zero, fail(401, "登录身份已失效")
	}
	var claims struct {
		AZP string `json:"azp"`
		IAT int64  `json:"iat"`
	}
	if id.Claims(&claims) != nil || claims.IAT > time.Now().Unix()+5 || (claims.AZP != "" && claims.AZP != s.cfg.ClientID) || (len(id.Audience) > 1 && claims.AZP != s.cfg.ClientID) {
		return zero, fail(401, "登录身份不匹配")
	}
	info, e := s.userInfo(r.Context(), token)
	if e != nil {
		return zero, e
	}
	if info.Sub != id.Subject {
		return zero, fail(401, "登录身份不匹配")
	}
	u := Identity{accountID(s.cfg.Issuer, info.Sub), info.Sub, info.Name, info.Picture}
	if e = s.record(r.Context(), u); e != nil {
		return zero, e
	}
	var disabled bool
	e = s.db.QueryRow(r.Context(), "SELECT disabled FROM backend_users WHERE account_id=$1", u.ID).Scan(&disabled)
	if e != nil {
		return zero, e
	}
	if disabled {
		return zero, fail(403, "本项目账号已停用")
	}
	if e = s.initializeAccount(r.Context(), u); e != nil {
		return zero, e
	}
	return u, nil
}
func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	defer func() {
		if recover() != nil {
			log.Print("request panicked")
			writeJSON(w, 500, map[string]string{"error": "服务暂不可用"})
		}
	}()
	var e error
	switch {
	case r.URL.Path == "/healthz":
		e = s.db.Ping(r.Context())
		if e == nil {
			writeJSON(w, 200, map[string]any{"ok": true, "backend": "go"})
		}
	case strings.HasPrefix(r.URL.Path, "/auth/"):
		e = s.authRoute(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/admin/"):
		if e = s.requireAdmin(w, r); e == nil {
			e = s.adminRoute(w, r)
		}
	case strings.HasPrefix(r.URL.Path, "/catalog-assets/"):
		e = s.publicAsset(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		origin := r.Header.Get("Origin")
		allowed := origin == "" || origin == s.cfg.Origin
		for _, o := range s.cfg.Origins {
			if o == origin {
				allowed = true
			}
		}
		if !allowed {
			e = fail(403, "来源不受允许")
			break
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Duoyu-Identity")
			w.WriteHeader(204)
			return
		}
		var u Identity
		u, e = s.authenticate(r)
		if e != nil {
			break
		}
		if strings.HasPrefix(r.URL.Path, "/v1/assets/") {
			e = s.assetRoute(w, r, u)
		} else if strings.HasPrefix(r.URL.Path, "/v1/templates/") && strings.HasSuffix(r.URL.Path, "/apply") {
			e = s.applyTemplate(w, r, u, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/templates/"), "/apply"))
		} else if strings.HasPrefix(r.URL.Path, "/v1/catalog/") {
			kind := strings.TrimPrefix(r.URL.Path, "/v1/catalog/")
			if r.Method != "GET" || !validKind(kind) {
				e = fail(404, "资源不存在")
			} else {
				e = s.rows(w, r, "SELECT id::text,kind,name,description,CASE WHEN kind='templates' AND payload->>'format'='duoyu-template' THEN jsonb_build_object('format','duoyu-template','version',1,'count',jsonb_array_length(payload->'entities')) ELSE payload END AS payload,version::text,updated_at FROM backend_catalog WHERE kind=$1 AND status='published' ORDER BY updated_at DESC LIMIT 100 OFFSET $2", kind, pageOffset(r))
			}
		} else {
			s.proxy.ServeHTTP(w, r)
		}
	case r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/"):
		rel := strings.TrimPrefix(r.URL.Path, "/admin/")
		if rel == "/admin" || rel == "" {
			rel = "index.html"
		}
		clean := filepath.Clean(rel)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			e = fail(404, "页面不存在")
			break
		}
		p := filepath.Join(s.cfg.AdminDist, clean)
		if _, err := os.Stat(p); err != nil {
			p = filepath.Join(s.cfg.AdminDist, "index.html")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data: blob:; connect-src 'self'; frame-ancestors 'none'")
		http.ServeFile(w, r, p)
	default:
		e = fail(404, "接口不存在")
	}
	if e != nil {
		var api apiError
		if errors.As(e, &api) {
			writeJSON(w, api.status, map[string]string{"error": api.message})
		} else {
			log.Printf("request failed: %s %s", r.Method, r.URL.Path)
			writeJSON(w, 500, map[string]string{"error": "服务暂不可用"})
		}
	}
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := Config{Origin: env("PUBLIC_ORIGIN", "https://sync.duoyu.link"), Issuer: env("DUOYU_ISSUER", "https://id.duoyu.link"), ClientID: os.Getenv("DUOYU_CLIENT_ID"), AdminClientID: os.Getenv("ADMIN_CLIENT_ID"), AdminSub: os.Getenv("SUPER_ADMIN_SUB"), AssetRoot: env("ASSET_ROOT", "./data/users"), AdminDist: env("ADMIN_DIST", "../../admin/dist"), BackupDir: env("BACKUP_DIR", "/www/backup/duoyu-sync"), Origins: strings.Split(os.Getenv("SYNC_ORIGINS"), ",")}
	var e error
	cfg.Key, e = hex.DecodeString(os.Getenv("SESSION_KEY"))
	if e != nil || len(cfg.Key) != 32 || cfg.AdminSub == "" || cfg.ClientID == "" || cfg.AdminClientID == "" {
		log.Fatal("Configure trusted super administrator, client IDs and session key")
	}
	db, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("Database configuration invalid")
	}
	defer db.Close()
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		sql, e := os.ReadFile("schema.sql")
		if e != nil {
			log.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(sql)); e != nil {
			log.Fatal(e)
		}
		return
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	oidctx := oidc.ClientContext(ctx, client)
	provider, e := oidc.NewProvider(oidctx, cfg.Issuer)
	if e != nil {
		log.Fatal("Identity discovery unavailable")
	}
	upstream, e := url.Parse(env("SYNC_UPSTREAM", "http://127.0.0.1:5260"))
	if e != nil || upstream.Hostname() != "127.0.0.1" {
		log.Fatal("Sync upstream must use loopback")
	}
	s := &Server{db: db, cfg: cfg, provider: provider, proxy: httputil.NewSingleHostReverseProxy(upstream), http: client}
	s.proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Access-Control-Allow-Origin")
		response.Header.Del("Vary")
		return nil
	}
	s.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		writeJSON(w, 502, map[string]string{"error": "同步服务暂不可用"})
	}
	if e = os.MkdirAll(cfg.AssetRoot, 0700); e != nil {
		log.Fatal(e)
	}
	server := &http.Server{Addr: env("LISTEN", "127.0.0.1:5261"), Handler: http.HandlerFunc(s.handler), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go s.jobs(ctx)
	go func() {
		<-ctx.Done()
		c, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		server.Shutdown(c)
	}()
	fmt.Println("DuoYu backend listening", server.Addr)
	if e = server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Fatal(e)
	}
}
