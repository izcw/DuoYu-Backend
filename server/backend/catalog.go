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
	preview, previewBytes, e := s.persistCatalogPreview(r.Context(), b)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	if e = s.persistFile("catalog", hash, b); e != nil {
		return e
	}
	_, e = s.db.Exec(r.Context(), "INSERT INTO backend_catalog_files(sha256,media_type,byte_size,preview_sha256,preview_bytes) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sha256) DO UPDATE SET preview_sha256=EXCLUDED.preview_sha256,preview_bytes=EXCLUDED.preview_bytes", hash, media, len(b), preview, previewBytes)
	if e != nil {
		return e
	}
	s.audit(r.Context(), "catalog.upload", hash)
	writeJSON(w, 200, map[string]string{"url": s.cfg.Origin + "/catalog-assets/" + hash, "preview_url": s.cfg.Origin + "/catalog-assets/" + hash + "?preview=1"})
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
	var media, preview string
	e := s.db.QueryRow(r.Context(), "SELECT media_type,coalesce(preview_sha256,''),EXISTS(SELECT 1 FROM backend_catalog WHERE status='published' AND payload->>'url'=$2) FROM backend_catalog_files WHERE sha256=$1", hash, s.cfg.Origin+"/catalog-assets/"+hash).Scan(&media, &preview, &published)
	if e != nil {
		return fail(404, "文件不存在")
	}
	if !published {
		if e = s.requireAdmin(w, r); e != nil {
			return fail(404, "文件不存在")
		}
	}
	if r.URL.Query().Get("preview") == "1" {
		hash, e = s.ensureCatalogPreview(r.Context(), hash, preview)
		if e != nil {
			return e
		}
		media = "image/jpeg"
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
	if published {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	http.ServeContent(w, r, hash, stat.ModTime(), file)
	return nil
}
