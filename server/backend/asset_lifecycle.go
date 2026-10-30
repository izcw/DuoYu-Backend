package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Logical deletion and physical deletion are separate, durable operations.
// Both upload and unlink hold the account row lock, so a concurrent re-upload
// cannot lose its newly written file to an older deletion request.
func (s *Server) enqueueAssetDeletion(ctx context.Context, tx pgx.Tx, id, hash string) error {
	if _, e := tx.Exec(ctx, "DELETE FROM sync_assets WHERE account_id=$1 AND sha256=$2", id, hash); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, "INSERT INTO sync_asset_deletions(account_id,sha256) VALUES($1,$2) ON CONFLICT DO NOTHING", id, hash)
	return e
}
func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request, id, hash string) error {
	force := r.URL.Query().Get("force") == "true"
	e := s.tx(r.Context(), id, func(tx pgx.Tx) error {
		if _, e := tx.Exec(r.Context(), "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", id); e != nil {
			return e
		}
		var exists bool
		if e := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", id, hash).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return nil
		} // Retry after a lost response is safe.
		rows, e := tx.Query(r.Context(), "SELECT entity_type,entity_id,payload FROM sync_entities WHERE account_id=$1 AND NOT deleted AND $2 IN(SELECT sync_asset_references(payload))", id, hash)
		if e != nil {
			return e
		}
		changes := []map[string]any{}
		for rows.Next() {
			var table, key string
			var payload []byte
			if e = rows.Scan(&table, &key, &payload); e != nil {
				rows.Close()
				return e
			}
			payload = bytes.ReplaceAll(payload, []byte("duoyu-asset:"+hash), []byte(""))
			changes = append(changes, map[string]any{"table": table, "id": key, "payload": json.RawMessage(payload), "deleted": table == "wallpapers"})
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		var conflicts bool
		if e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM sync_conflicts WHERE account_id=$1 AND resolved_at IS NULL AND $2 IN(SELECT sync_asset_references(detail)))", id, hash).Scan(&conflicts); e != nil {
			return e
		}
		if (len(changes) > 0 || conflicts) && !force {
			return fail(409, "图片仍在使用，请确认移除所有引用后再删除")
		}

		wallpaperRemoved := false
		for _, c := range changes {
			if c["table"] == "wallpapers" {
				wallpaperRemoved = true
			}
		}
		if wallpaperRemoved {
			var payload []byte
			e = tx.QueryRow(r.Context(), "SELECT payload FROM sync_entities WHERE account_id=$1 AND entity_type='preferences' AND entity_id='wallpaperMode' AND NOT deleted", id).Scan(&payload)
			if e == nil {
				var p map[string]any
				if json.Unmarshal(payload, &p) != nil {
					return fail(500, "壁纸配置无效")
				}
				p["value"] = nil
				changes = append(changes, map[string]any{"table": "preferences", "id": "wallpaperMode", "payload": p, "deleted": false})
			} else if e != pgx.ErrNoRows {
				return e
			}
		}
		if len(changes) > 0 {
			var revision int64
			if e = tx.QueryRow(r.Context(), "UPDATE sync_accounts SET head_revision=head_revision+1 WHERE id=$1 RETURNING head_revision", id).Scan(&revision); e != nil {
				return e
			}
			for _, change := range changes {
				change["revision"] = strconv.FormatInt(revision, 10)
				if _, e = tx.Exec(r.Context(), "UPDATE sync_entities SET payload=$4,deleted=$5,revision=$6 WHERE account_id=$1 AND entity_type=$2 AND entity_id=$3", id, change["table"], change["id"], change["payload"], change["deleted"], revision); e != nil {
					return e
				}
			}
			body, _ := json.Marshal(changes)
			if _, e = tx.Exec(r.Context(), "INSERT INTO sync_changes(account_id,revision,operation_id,changes) VALUES($1,$2,$3,$4)", id, revision, newUUID(), body); e != nil {
				return e
			}
		}
		return s.enqueueAssetDeletion(r.Context(), tx, id, hash)
	})
	if e != nil {
		return e
	}
	if e = s.audit(r.Context(), "asset.delete", id+":"+hash); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"ok": true, "physical_cleanup": "queued"})
	return nil
}

func (s *Server) cleanupAssets(ctx context.Context) error {
	// Shared with the backup process: its database snapshot and file archive must
	// not straddle a physical deletion. Busy backups defer collection, not uploads.
	conn, e := s.db.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(52615262)").Scan(&locked); e != nil || !locked {
		return e
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(52615262)")
	rows, e := conn.Query(ctx, "SELECT account_id::text FROM sync_assets UNION SELECT account_id::text FROM sync_asset_deletions")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e = s.tx(ctx, id, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, "SELECT sync_reconcile_assets($1,true)", id); e != nil {
				return e
			}
			pending, e := tx.Query(ctx, "SELECT sha256 FROM sync_asset_deletions WHERE account_id=$1 ORDER BY requested_at LIMIT 200", id)
			if e != nil {
				return e
			}
			hashes := []string{}
			for pending.Next() {
				var hash string
				if e = pending.Scan(&hash); e != nil {
					pending.Close()
					return e
				}
				hashes = append(hashes, hash)
			}
			pending.Close()
			if e = pending.Err(); e != nil {
				return e
			}
			for _, hash := range hashes {
				if !hashPattern.MatchString(hash) {
					return fail(500, "清理队列文件标识无效")
				}
				var restored bool
				if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", id, hash).Scan(&restored); e != nil {
					return e
				}
				if !restored {
					if e = os.Remove(filepath.Join(s.cfg.AssetRoot, s.relativeFile(id, hash))); e != nil && !os.IsNotExist(e) {
						return e
					}
				}
				if _, e = tx.Exec(ctx, "DELETE FROM sync_asset_deletions WHERE account_id=$1 AND sha256=$2", id, hash); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return s.cleanupOrphanFiles(ctx)
}
func (s *Server) cleanupOrphanFiles(ctx context.Context) error {
	dirs, e := os.ReadDir(s.cfg.AssetRoot)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, dir := range dirs {
		if !dir.IsDir() || !uuidPattern.MatchString(dir.Name()) {
			continue
		}
		id := dir.Name()
		if e = s.tx(ctx, id, func(tx pgx.Tx) error {
			var exists bool
			if e := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_accounts WHERE id=$1)", id).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return nil
			}
			if _, e := tx.Exec(ctx, "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", id); e != nil {
				return e
			}
			files, e := os.ReadDir(filepath.Join(s.cfg.AssetRoot, id))
			if e != nil {
				return e
			}
			for _, file := range files {
				if file.IsDir() || file.Type()&os.ModeSymlink != 0 || (!hashPattern.MatchString(file.Name()) && !strings.HasPrefix(file.Name(), ".upload-")) {
					continue
				}
				stat, e := file.Info()
				if e != nil {
					return e
				}
				if !stat.Mode().IsRegular() || time.Since(stat.ModTime()) < 24*time.Hour {
					continue
				}
				if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", id, file.Name()).Scan(&exists); e != nil {
					return e
				}
				if !exists {
					if e = os.Remove(filepath.Join(s.cfg.AssetRoot, id, file.Name())); e != nil && !os.IsNotExist(e) {
						return e
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return nil
}
