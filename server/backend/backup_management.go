package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Allocate a revision even for unchanged data. The sync_changes trigger writes
// the bounded snapshot in the same transaction as the account revision.
func (s *Server) createOwnBackup(w http.ResponseWriter, r *http.Request, u Identity) error {
	ctx := r.Context()
	var revision int64
	var id string
	err := s.tx(ctx, u.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "UPDATE sync_accounts SET head_revision=head_revision+1 WHERE id=$1 RETURNING head_revision", u.ID).Scan(&revision); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO sync_changes(account_id,revision,operation_id,changes) VALUES($1,$2,gen_random_uuid(),'[]'::jsonb)", u.ID, revision); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT id::text FROM sync_user_backups WHERE account_id=$1 AND revision=$2 AND epoch=(SELECT epoch FROM sync_generation WHERE singleton)", u.ID, revision).Scan(&id)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "revision": fmt.Sprint(revision)})
	return nil
}

// The public gateway serves the account's list directly from the same records as admin.
func (s *Server) listOwnBackups(w http.ResponseWriter, r *http.Request, u Identity) error {
	type entry struct {
		ID          string    `json:"id"`
		Revision    string    `json:"revision"`
		Epoch       string    `json:"epoch"`
		CreatedAt   time.Time `json:"created_at"`
		EntityCount int       `json:"entity_count"`
	}
	items := []entry{}
	err := s.tx(r.Context(), u.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id::text,revision::text,epoch::text,created_at,jsonb_array_length(snapshot->'entities') FROM sync_user_backups WHERE account_id=$1 ORDER BY created_at DESC,id DESC LIMIT 5`, u.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item entry
			if err := rows.Scan(&item.ID, &item.Revision, &item.Epoch, &item.CreatedAt, &item.EntityCount); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"backups": items})
	return nil
}

func (s *Server) backupManagement(w http.ResponseWriter, r *http.Request, path string) error {
	ctx := r.Context()
	if path == "backups/users" {
		return s.rows(w, r, `SELECT a.id::text,a.subject,coalesce(u.name,'') AS name,coalesce(u.picture,'') AS picture,count(b.id)::int AS backup_count,max(b.created_at) AS latest_backup,count(*) OVER()::int AS total FROM sync_accounts a LEFT JOIN backend_users u ON u.account_id=a.id LEFT JOIN sync_user_backups b ON b.account_id=a.id WHERE coalesce(u.name,'') ILIKE $1 OR a.subject ILIKE $1 GROUP BY a.id,u.name,u.picture ORDER BY max(b.created_at) DESC NULLS LAST,a.id LIMIT 20 OFFSET $2`, "%"+r.URL.Query().Get("q")+"%", pageOffset(r))
	}
	if strings.HasPrefix(path, "backups/users/") {
		parts := strings.Split(path, "/")
		if len(parts) < 3 || !uuidPattern.MatchString(parts[2]) {
			return fail(400, "用户ID无效")
		}
		id := parts[2]
		if len(parts) == 3 {
			return s.rows(w, r, `SELECT id::text,revision::text,epoch::text,created_at,jsonb_array_length(snapshot->'entities') AS entity_count,pg_column_size(snapshot)::bigint AS bytes,count(*) OVER()::int AS total FROM sync_user_backups WHERE account_id=$1 ORDER BY created_at DESC,id DESC LIMIT 5`, id)
		}
		if len(parts) != 4 || !uuidPattern.MatchString(parts[3]) {
			return fail(400, "备份ID无效")
		}
		conn, e := s.db.Acquire(ctx)
		if e != nil {
			return e
		}
		defer conn.Release()
		if _, e = conn.Exec(ctx, "SELECT pg_advisory_lock(52615262)"); e != nil {
			return e
		}
		defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(52615262)")
		var raw []byte
		if e = conn.QueryRow(ctx, "SELECT snapshot FROM sync_user_backups WHERE account_id=$1 AND id=$2", id, parts[3]).Scan(&raw); e == pgx.ErrNoRows {
			return fail(404, "备份不存在或已轮换")
		}
		if e != nil {
			return e
		}
		var snapshot struct {
			Assets map[string]struct {
				Body *string `json:"body"`
			} `json:"assets"`
		}
		if e = json.Unmarshal(raw, &snapshot); e != nil {
			return e
		}
		// Preflight all binary resources before sending headers; a partial snapshot is not a backup.
		files := map[string]string{}
		for hash, asset := range snapshot.Assets {
			if !hashPattern.MatchString(hash) {
				return fail(500, "备份资源标识无效")
			}
			if asset.Body != nil {
				continue
			}
			file := filepath.Join(s.cfg.AssetRoot, id, hash)
			st, err := os.Lstat(file)
			if err != nil || !st.Mode().IsRegular() {
				return fail(503, "备份图片暂不可读取，请检查文件存储")
			}
			f, err := os.Open(file)
			if err != nil {
				return fail(503, "备份图片暂不可读取")
			}
			f.Close()
			files[hash] = file
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="user-%s-%s.zip"`, id, parts[3]))
		z := zip.NewWriter(w)
		entry, e := z.Create("snapshot.json")
		if e != nil {
			return e
		}
		if _, e = entry.Write(raw); e != nil {
			return e
		}
		for hash, asset := range snapshot.Assets {
			entry, e = z.CreateHeader(&zip.FileHeader{Name: "assets/" + hash, Method: zip.Store})
			if e != nil {
				return e
			}
			if asset.Body != nil {
				b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(*asset.Body, "\n", ""))
				if err != nil {
					return err
				}
				_, e = io.Copy(entry, bytes.NewReader(b))
			} else {
				f, err := os.Open(files[hash])
				if err != nil {
					return err
				}
				_, e = io.Copy(entry, f)
				f.Close()
			}
			if e != nil {
				return e
			}
		}
		if e = z.Close(); e != nil {
			return e
		}
		s.audit(ctx, "backup.user.download", id+":"+parts[3])
		return nil
	}
	if path == "backups/system" || strings.HasPrefix(path, "backups/system/") {
		return s.systemBackupRoute(w, r, path)
	}
	return fail(404, "接口不存在")
}

type backupPart struct {
	Object string `json:"object"`
	Bytes  int64  `json:"bytes"`
}
type backupFile struct {
	Path  string       `json:"path"`
	Parts []backupPart `json:"parts"`
}
type systemBackup struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	CreatedAt  time.Time    `json:"created_at"`
	Bytes      int64        `json:"bytes"`
	AddedBytes int64        `json:"added_bytes"`
	Files      []backupFile `json:"files"`
}

var backupObjectPattern = regexp.MustCompile(`^[a-f0-9-]{36}/[a-f0-9]{64}\.enc$`)

func (s *Server) systemBackupRoute(w http.ResponseWriter, r *http.Request, path string) error {
	conn, e := s.db.Acquire(r.Context())
	if e != nil {
		return e
	}
	defer conn.Release()
	if _, e = conn.Exec(r.Context(), "SELECT pg_advisory_lock(52615262)"); e != nil {
		return e
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(52615262)")
	if path == "backups/system" {
		entries, e := os.ReadDir(s.cfg.BackupDir)
		if e != nil {
			return fail(503, "备份目录不可读取")
		}
		records := []map[string]any{}
		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".record.json")
			if !strings.HasSuffix(entry.Name(), ".record.json") || !uuidPattern.MatchString(name) || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(s.cfg.BackupDir, entry.Name()))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fail(503, "备份记录不可读取")
			}
			var record systemBackup
			if json.Unmarshal(raw, &record) != nil || record.ID != name {
				return fail(503, "备份记录损坏，请检查备份日志")
			}
			records = append(records, map[string]any{"id": record.ID, "kind": record.Kind, "created_at": record.CreatedAt, "bytes": record.Bytes, "added_bytes": record.AddedBytes})
		}
		sort.Slice(records, func(i, j int) bool {
			return records[i]["created_at"].(time.Time).After(records[j]["created_at"].(time.Time))
		})
		writeJSON(w, 200, map[string]any{"items": records})
		return nil
	}
	id := strings.TrimPrefix(path, "backups/system/")
	if !uuidPattern.MatchString(id) {
		return fail(400, "备份ID无效")
	}
	raw, e := os.ReadFile(filepath.Join(s.cfg.BackupDir, id+".record.json"))
	if os.IsNotExist(e) {
		return fail(404, "备份不存在或已轮换")
	}
	if e != nil {
		return e
	}
	var record systemBackup
	if json.Unmarshal(raw, &record) != nil || record.ID != id {
		return fail(503, "备份记录损坏")
	}
	objects := map[string]bool{}
	for _, file := range record.Files {
		for _, part := range file.Parts {
			if !backupObjectPattern.MatchString(part.Object) {
				return fail(503, "备份文件路径无效")
			}
			if objects[part.Object] {
				continue
			}
			p := filepath.Join(s.cfg.BackupDir, "objects", part.Object)
			for _, check := range []string{filepath.Dir(p), p} {
				st, err := os.Lstat(check)
				if err != nil || st.Mode()&os.ModeSymlink != 0 {
					return fail(503, "备份文件缺失")
				}
			}
			f, err := os.Open(p)
			if err != nil {
				return fail(503, "备份文件不可读取")
			}
			st, err := f.Stat()
			f.Close()
			if err != nil || !st.Mode().IsRegular() {
				return fail(503, "备份文件无效")
			}
			objects[part.Object] = true
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="system-`+id+`.zip"`)
	z := zip.NewWriter(w)
	entry, e := z.Create(id + ".record.json")
	if e != nil {
		return e
	}
	if _, e = entry.Write(raw); e != nil {
		return e
	}
	for name := range objects {
		f, err := os.Open(filepath.Join(s.cfg.BackupDir, "objects", name))
		if err != nil {
			return err
		}
		entry, e = z.CreateHeader(&zip.FileHeader{Name: "objects/" + name, Method: zip.Store})
		if e != nil {
			return e
		}
		_, e = io.Copy(entry, f)
		f.Close()
		if e != nil {
			return e
		}
	}
	if e = z.Close(); e != nil {
		return e
	}
	s.audit(r.Context(), "backup.system.download", id)
	return nil
}
