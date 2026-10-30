package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateImage(b []byte, t string) error {
	valid := false
	switch t {
	case "image/png":
		valid = bytes.HasPrefix(b, []byte{137, 80, 78, 71, 13, 10, 26, 10})
	case "image/jpeg":
		valid = len(b) > 2 && b[0] == 255 && b[1] == 216
	case "image/gif":
		valid = bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))
	case "image/webp":
		valid = len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}
	if !valid {
		return fail(415, "仅支持 PNG、JPEG、GIF、WebP 图片")
	}
	return nil
}
func (s *Server) relativeFile(id, hash string) string { return filepath.Join(id, hash) }
func (s *Server) persistFile(id, hash string, b []byte) error {
	dir := filepath.Join(s.cfg.AssetRoot, id)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(dir, ".upload-")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if e = tmp.Chmod(0600); e == nil {
		_, e = tmp.Write(b)
	}
	if e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(dir, hash))
}
func (s *Server) quota(ctx context.Context, tx pgx.Tx, id string) (int64, error) {
	var n int64
	e := tx.QueryRow(ctx, "SELECT COALESCE(u.quota_bytes,(SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes')) FROM backend_users u WHERE account_id=$1", id).Scan(&n)
	return n, e
}
func (s *Server) assetRoute(w http.ResponseWriter, r *http.Request, u Identity) error {
	hash := r.URL.Path[len("/v1/assets/"):]
	if !hashPattern.MatchString(hash) {
		return fail(400, "文件标识不正确")
	}
	if r.Method == "POST" {
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
		if e != nil {
			return fail(413, "单张图片不能超过20MB")
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != hash {
			return fail(400, "图片校验失败")
		}
		media := r.Header.Get("Content-Type")
		if e = validateImage(b, media); e != nil {
			return e
		}
		e = s.tx(r.Context(), u.ID, func(tx pgx.Tx) error {
			if _, e := tx.Exec(r.Context(), "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", u.ID); e != nil {
				return e
			}
			var used int64
			var exists bool
			if e := tx.QueryRow(r.Context(), "SELECT COALESCE(sum(byte_size),0),COALESCE(bool_or(sha256=$2),false) FROM sync_assets WHERE account_id=$1", u.ID, hash).Scan(&used, &exists); e != nil {
				return e
			}
			quota, e := s.quota(r.Context(), tx, u.ID)
			if e != nil {
				return e
			}
			if !exists && used+int64(len(b)) > quota {
				return fail(413, "asset_quota")
			}
			if e = s.persistFile(u.ID, hash, b); e != nil {
				return e
			}
			_, e = tx.Exec(r.Context(), "INSERT INTO sync_assets(account_id,sha256,media_type,body,byte_size,file_path) VALUES($1,$2,$3,NULL,$4,$5) ON CONFLICT(account_id,sha256) DO UPDATE SET body=NULL,byte_size=excluded.byte_size,file_path=excluded.file_path,last_uploaded_at=now()", u.ID, hash, media, len(b), s.relativeFile(u.ID, hash))
			return e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"sha256": hash})
		return nil
	}
	if r.Method == "GET" {
		return s.serveAsset(w, r, u.ID, hash)
	}
	return fail(405, "方法不支持")
}
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, id, hash string) error {
	var body []byte
	var media string
	var path *string
	e := s.tx(r.Context(), id, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), "SELECT media_type,body,file_path FROM sync_assets WHERE account_id=$1 AND sha256=$2", id, hash).Scan(&media, &body, &path)
	})
	if e == pgx.ErrNoRows {
		return fail(404, "图片不存在")
	}
	if e != nil {
		return e
	}
	if path != nil {
		expected := s.relativeFile(id, hash)
		if *path != expected {
			return fmt.Errorf("invalid stored asset path")
		}
		file, e := os.Open(filepath.Join(s.cfg.AssetRoot, expected))
		if e != nil {
			return fail(503, "图片文件暂不可用")
		}
		defer file.Close()
		stat, e := file.Stat()
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeContent(w, r, hash, stat.ModTime(), file)
		return nil
	}
	if len(body) == 0 {
		return fail(404, "图片不存在")
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(body)
	return nil
}

// Migration preserves hash references and keeps PostgreSQL bytes until each file is durably written.
func (s *Server) migrateAssets(ctx context.Context) error {
	for {
		var id, hash string
		var body []byte
		e := s.db.QueryRow(ctx, "SELECT account_id::text,sha256,body FROM sync_assets WHERE body IS NOT NULL LIMIT 1").Scan(&id, &hash, &body)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("asset integrity check failed")
		}
		e = s.tx(ctx, id, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", id); e != nil {
				return e
			}
			if e := s.persistFile(id, hash, body); e != nil {
				return e
			}
			_, e := tx.Exec(ctx, "UPDATE sync_assets SET body=NULL,byte_size=$3,file_path=$4 WHERE account_id=$1 AND sha256=$2", id, hash, len(body), s.relativeFile(id, hash))
			return e
		})
		if e != nil {
			return e
		}
	}
}
