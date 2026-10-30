package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) uploadCatalog(w http.ResponseWriter, r *http.Request) error {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
	if e != nil {
		return fail(413, "图片不能超过20MB")
	}
	media := r.Header.Get("Content-Type")
	if e = validateImage(b, media); e != nil {
		return e
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	if e = s.persistFile("catalog", hash, b); e != nil {
		return e
	}
	_, e = s.db.Exec(r.Context(), "INSERT INTO backend_catalog_files(sha256,media_type,byte_size) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", hash, media, len(b))
	if e != nil {
		return e
	}
	s.audit(r.Context(), "catalog.upload", hash)
	writeJSON(w, 200, map[string]string{"url": s.cfg.Origin + "/catalog-assets/" + hash})
	return nil
}
func (s *Server) publicAsset(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "GET" && r.Method != "HEAD" {
		return fail(405, "方法不支持")
	}
	hash := strings.TrimPrefix(r.URL.Path, "/catalog-assets/")
	if !hashPattern.MatchString(hash) {
		return fail(404, "文件不存在")
	}
	var published bool
	var media string
	e := s.db.QueryRow(r.Context(), "SELECT media_type,EXISTS(SELECT 1 FROM backend_catalog WHERE status='published' AND payload->>'url'=$2) FROM backend_catalog_files WHERE sha256=$1", hash, s.cfg.Origin+"/catalog-assets/"+hash).Scan(&media, &published)
	if e != nil || !published {
		return fail(404, "文件不存在")
	}
	file, e := os.Open(filepath.Join(s.cfg.AssetRoot, "catalog", hash))
	if e != nil {
		return fail(404, "文件不存在")
	}
	defer file.Close()
	stat, e := file.Stat()
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, hash, stat.ModTime(), file)
	return nil
}
