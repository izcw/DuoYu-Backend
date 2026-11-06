package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Serve only the administrator's current published default. Never expose the
// source account, drafts, previous defaults or arbitrary catalog files.
func (s *Server) guestTemplate(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return fail(405, "只支持读取模板")
	}
	var raw []byte
	var revision string
	err := s.db.QueryRow(r.Context(), `SELECT c.payload, c.id::text || ':' || c.version::text FROM backend_settings b JOIN backend_catalog c ON c.id::text=(b.value#>>'{}') WHERE b.id='default_template_id' AND c.kind='templates' AND c.status='published'`).Scan(&raw, &revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	snap := templateSnapshot{Entities: []templateEntity{}}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &snap); err != nil {
			return err
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	if r.URL.Path == "/api/v1/guest-template" {
		writeJSON(w, 200, map[string]any{"configured": len(raw) > 0, "entities": snap.Entities, "revision": revision})
		return nil
	}
	hash := strings.TrimPrefix(r.URL.Path, "/api/v1/guest-template/assets/")
	if hashPattern.MatchString(hash) {
		for _, asset := range snap.Assets {
			if asset.Hash != hash {
				continue
			}
			file, e := os.Open(filepath.Join(s.cfg.AssetRoot, "catalog", hash))
			if e != nil {
				return fail(404, "图片不可用")
			}
			defer file.Close()
			stat, e := file.Stat()
			if e != nil {
				return e
			}
			w.Header().Set("Content-Type", asset.Media)
			http.ServeContent(w, r, hash, stat.ModTime(), file)
			return nil
		}
	}
	return fail(404, "模板图片不存在")
}
