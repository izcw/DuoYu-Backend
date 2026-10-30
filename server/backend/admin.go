package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var backupPattern = regexp.MustCompile(`^sync-[0-9TZ:.\-]+\.(?:dump|assets\.tar)\.enc$`)

func pageOffset(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if n < 1 {
		n = 1
	}
	if n > 100000 {
		n = 100000
	}
	return (n - 1) * 20
}
func (s *Server) rows(w http.ResponseWriter, r *http.Request, query string, args ...any) error {
	rows, e := s.db.Query(r.Context(), query, args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		values, e := rows.Values()
		if e != nil {
			return e
		}
		item := map[string]any{}
		for i, f := range rows.FieldDescriptions() {
			item[string(f.Name)] = values[i]
		}
		out = append(out, item)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": out})
	return nil
}
func (s *Server) adminRoute(w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")
	ctx := r.Context()
	if r.Method == "POST" && path == "templates/capture" {
		return s.captureTemplate(w, r)
	}
	if r.Method == "POST" && path == "templates/default" {
		return s.setDefaultTemplate(w, r)
	}
	if r.Method == "POST" && path == "catalog-assets" {
		return s.uploadCatalog(w, r)
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "users/") {
		parts := strings.Split(path, "/")
		if len(parts) == 4 && parts[2] == "files" && uuidPattern.MatchString(parts[1]) && hashPattern.MatchString(parts[3]) {
			return s.deleteAsset(w, r, parts[1], parts[3])
		}
	}
	if r.Method == "GET" {
		switch path {
		case "me":
			writeJSON(w, 200, map[string]any{"account_id": "8888888888", "csrf": ctx.Value(csrfContext{}), "role": "super_admin", "profile": ctx.Value(profileContext{})})
			return nil
		case "overview":
			var data []byte
			e := s.db.QueryRow(ctx, `SELECT json_build_object('users',(SELECT count(*) FROM sync_accounts),'entities',(SELECT count(*) FROM sync_entities WHERE NOT deleted),'storage_bytes',(SELECT COALESCE(sum(byte_size),0) FROM sync_assets),'conflicts',(SELECT count(*) FROM sync_conflicts WHERE resolved_at IS NULL),'operations',(SELECT count(*) FROM sync_receipts),'catalog',(SELECT count(*) FROM backend_catalog WHERE status='published'))`).Scan(&data)
			if e != nil {
				return e
			}
			var value any
			json.Unmarshal(data, &value)
			writeJSON(w, 200, value)
			return nil
		case "charts":
			return s.rows(w, r, `SELECT d::date::text AS day,count(c.account_id)::int AS operations,count(DISTINCT c.account_id)::int AS users FROM generate_series(current_date-29,current_date,interval '1 day') d LEFT JOIN sync_changes c ON c.created_at>=d AND c.created_at<d+interval '1 day' GROUP BY d ORDER BY d`)
		case "users":
			return s.rows(w, r, `SELECT a.id::text,a.subject,COALESCE(u.name,'') AS name,COALESCE(u.picture,'') AS picture,a.head_revision::text,COALESCE(u.disabled,false) AS disabled,u.quota_bytes,u.last_seen,(SELECT COALESCE(sum(byte_size),0)::text FROM sync_assets f WHERE f.account_id=a.id) AS storage_bytes,count(*) OVER()::int AS total FROM sync_accounts a LEFT JOIN backend_users u ON u.account_id=a.id WHERE a.subject ILIKE $1 OR COALESCE(u.name,'') ILIKE $1 OR a.id::text ILIKE $1 ORDER BY u.last_seen DESC NULLS LAST,a.id LIMIT 20 OFFSET $2`, "%"+r.URL.Query().Get("q")+"%", pageOffset(r))
		case "settings":
			return s.rows(w, r, "SELECT id,value FROM backend_settings ORDER BY id")
		case "audit":
			return s.rows(w, r, "SELECT id::text,actor,action,target,created_at,count(*) OVER()::int AS total FROM backend_audit ORDER BY id DESC LIMIT 20 OFFSET $1", pageOffset(r))
		case "jobs":
			return s.rows(w, r, "SELECT id::text,kind,status,error,created_at,finished_at,count(*) OVER()::int AS total FROM backend_jobs ORDER BY created_at DESC LIMIT 20 OFFSET $1", pageOffset(r))
		case "backups":
			entries, e := os.ReadDir(s.cfg.BackupDir)
			if e != nil {
				return fail(503, "备份目录不可读取，请检查后端账号的目录访问权限")
			}
			items := []map[string]any{}
			for _, entry := range entries {
				if entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() && backupPattern.MatchString(entry.Name()) {
					stat, e := entry.Info()
					if os.IsNotExist(e) {
						continue
					}
					if e != nil {
						return fail(503, "备份文件状态暂时无法读取，请重试")
					}
					if !stat.Mode().IsRegular() {
						continue
					}
					items = append(items, map[string]any{"name": entry.Name(), "bytes": stat.Size(), "modified": stat.ModTime()})
				}
			}
			sort.Slice(items, func(i, j int) bool { return items[i]["modified"].(time.Time).After(items[j]["modified"].(time.Time)) })
			total := len(items)
			start := pageOffset(r)
			if start > total {
				start = total
			}
			end := start + 20
			if end > total {
				end = total
			}
			items = items[start:end]
			for _, item := range items {
				item["total"] = total
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		if strings.HasPrefix(path, "backups/") {
			name := strings.TrimPrefix(path, "backups/")
			if !backupPattern.MatchString(name) {
				return fail(400, "文件名无效")
			}
			file := filepath.Join(s.cfg.BackupDir, name)
			stat, e := os.Lstat(file)
			if os.IsNotExist(e) {
				return fail(404, "备份不存在")
			}
			if e != nil {
				return fail(503, "备份文件不可读取，请检查后端账号访问权限")
			}
			if !stat.Mode().IsRegular() {
				return fail(404, "备份不存在")
			}
			stream, e := os.Open(file)
			if os.IsNotExist(e) {
				return fail(404, "备份不存在")
			}
			if e != nil {
				return fail(503, "备份文件不可读取，请检查后端账号访问权限")
			}
			defer stream.Close()
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			s.audit(ctx, "backup.download", name)
			http.ServeContent(w, r, name, stat.ModTime(), stream)
			return nil
		}
		if strings.HasPrefix(path, "users/") {
			parts := strings.Split(path, "/")
			if len(parts) < 2 || !uuidPattern.MatchString(parts[1]) {
				return fail(400, "用户ID无效")
			}
			id := parts[1]
			var exists bool
			if e := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_accounts WHERE id=$1)", id).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return fail(404, "用户不存在")
			}
			if len(parts) == 3 && parts[2] == "settings" {
				return s.rows(w, r, "SELECT a.id::text,COALESCE(u.name,'') AS name,COALESCE(u.picture,'') AS picture,COALESCE(u.disabled,false) AS disabled,u.quota_bytes,COALESCE((SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes'),104857600)::text AS default_quota_bytes,COALESCE(u.quota_bytes,(SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes'),104857600)::text AS effective_quota_bytes,(SELECT COALESCE(sum(byte_size),0)::text FROM sync_assets WHERE account_id=a.id) AS storage_bytes FROM sync_accounts a LEFT JOIN backend_users u ON u.account_id=a.id WHERE a.id=$1", id)
			}
			if len(parts) == 3 && parts[2] == "files" {
				return s.rows(w, r, "WITH entity_refs AS (SELECT h,count(*)::int n FROM sync_entities e CROSS JOIN LATERAL (SELECT DISTINCT sync_asset_references(e.payload) h) refs WHERE e.account_id=$1 AND NOT e.deleted GROUP BY h),conflict_refs AS (SELECT h,count(*)::int n FROM sync_conflicts c CROSS JOIN LATERAL (SELECT DISTINCT sync_asset_references(c.detail) h) refs WHERE c.account_id=$1 AND c.resolved_at IS NULL GROUP BY h) SELECT a.sha256,a.media_type,a.byte_size::text,a.created_at,a.last_uploaded_at,a.referenced_at,COALESCE(e.n,0) AS reference_count,COALESCE(c.n,0) AS conflict_count,count(*) OVER()::int AS total FROM sync_assets a LEFT JOIN entity_refs e ON e.h=a.sha256 LEFT JOIN conflict_refs c ON c.h=a.sha256 WHERE a.account_id=$1 ORDER BY a.created_at DESC LIMIT 20 OFFSET $2", id, pageOffset(r))
			}
			if len(parts) == 4 && parts[2] == "files" && hashPattern.MatchString(parts[3]) {
				s.audit(ctx, "asset.read", id+":"+parts[3])
				return s.serveAsset(w, r, id, parts[3])
			}
			if len(parts) == 3 && parts[2] == "sync" {
				return s.rows(w, r, "SELECT revision::text,operation_id::text,created_at,jsonb_array_length(changes)::int AS changes,count(*) OVER()::int AS total FROM sync_changes WHERE account_id=$1 ORDER BY revision DESC LIMIT 20 OFFSET $2", id, pageOffset(r))
			}
			if len(parts) == 3 && parts[2] == "conflicts" {
				return s.rows(w, r, "SELECT operation_id::text,detail,created_at,resolved_at,count(*) OVER()::int AS total FROM sync_conflicts WHERE account_id=$1 ORDER BY created_at DESC LIMIT 20 OFFSET $2", id, pageOffset(r))
			}
		}
		if strings.HasPrefix(path, "catalog/") {
			parts := strings.Split(path, "/")
			if len(parts) == 2 && validKind(parts[1]) {
				return s.rows(w, r, "SELECT id::text,kind,name,description,status,payload,version::text,updated_at,count(*) OVER()::int AS total FROM backend_catalog WHERE kind=$1 AND name ILIKE $2 ORDER BY updated_at DESC LIMIT 20 OFFSET $3", parts[1], "%"+r.URL.Query().Get("q")+"%", pageOffset(r))
			}
			if len(parts) == 3 && validKind(parts[1]) && uuidPattern.MatchString(parts[2]) {
				return s.rows(w, r, "SELECT id::text,kind,name,description,status,payload,version::text FROM backend_catalog WHERE kind=$1 AND id=$2", parts[1], parts[2])
			}
		}
	}
	if r.Method == "POST" {
		if path == "settings" {
			var in struct {
				QuotaMB int64 `json:"quota_mb"`
			}
			if e := decode(w, r, &in); e != nil {
				return e
			}
			if in.QuotaMB < 1 || in.QuotaMB > 102400 {
				return fail(400, "默认配额范围为1–102400MB")
			}
			var required int64
			if e := s.db.QueryRow(ctx, "SELECT COALESCE(sum((asset->>'bytes')::bigint),0) FROM backend_catalog c CROSS JOIN LATERAL jsonb_array_elements(COALESCE(c.payload->'assets','[]'::jsonb)) asset WHERE c.id::text=(SELECT value#>>'{}' FROM backend_settings WHERE id='default_template_id')").Scan(&required); e != nil {
				return e
			}
			if required > in.QuotaMB*1048576 {
				return fail(409, "官方默认模板图片超过这个配额，请先更换模板或提高配额")
			}
			_, e := s.db.Exec(ctx, "UPDATE backend_settings SET value=to_jsonb($1::bigint) WHERE id='storage_quota_bytes'", in.QuotaMB*1048576)
			if e != nil {
				return e
			}
			if e = s.audit(ctx, "settings.quota", strconv.FormatInt(in.QuotaMB, 10)); e != nil {
				return e
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
			return nil
		}
		if path == "jobs" {
			var in struct {
				Kind string `json:"kind"`
			}
			if e := decode(w, r, &in); e != nil {
				return e
			}
			if in.Kind != "backup" && in.Kind != "asset-migration" {
				return fail(400, "任务无效")
			}
			var id string
			e := s.db.QueryRow(ctx, "INSERT INTO backend_jobs(kind) SELECT $1 WHERE NOT EXISTS(SELECT 1 FROM backend_jobs WHERE status IN ('queued','running') AND kind=$1) RETURNING id::text", in.Kind).Scan(&id)
			if e != nil {
				return fail(409, "同类任务正在运行")
			}
			s.audit(ctx, "job.create", in.Kind+":"+id)
			writeJSON(w, 202, map[string]string{"id": id})
			return nil
		}
		if strings.HasPrefix(path, "users/") {
			id := strings.TrimPrefix(path, "users/")
			if !uuidPattern.MatchString(id) {
				return fail(400, "用户ID无效")
			}
			var in struct {
				Disabled bool   `json:"disabled"`
				QuotaMB  *int64 `json:"quota_mb"`
			}
			if e := decode(w, r, &in); e != nil {
				return e
			}
			var quota *int64
			if in.QuotaMB != nil {
				if *in.QuotaMB < 0 || *in.QuotaMB > 102400 {
					return fail(400, "配额无效")
				}
				n := *in.QuotaMB * 1048576
				quota = &n
			}
			var sub string
			if e := s.db.QueryRow(ctx, "SELECT subject FROM sync_accounts WHERE id=$1", id).Scan(&sub); e != nil {
				return fail(404, "用户不存在")
			}
			if sub == s.cfg.AdminSub && in.Disabled {
				return fail(400, "不能停用超级管理员")
			}
			_, e := s.db.Exec(ctx, "INSERT INTO backend_users(account_id,disabled,quota_bytes) VALUES($1,$2,$3) ON CONFLICT(account_id) DO UPDATE SET disabled=excluded.disabled,quota_bytes=excluded.quota_bytes", id, in.Disabled, quota)
			if e != nil {
				return e
			}
			s.audit(ctx, "user.update", id)
			writeJSON(w, 200, map[string]bool{"ok": true})
			return nil
		}
		if strings.HasPrefix(path, "catalog/") {
			parts := strings.Split(path, "/")
			if len(parts) < 2 || !validKind(parts[1]) {
				return fail(400, "资源类型无效")
			}
			var in struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Status      string          `json:"status"`
				Payload     json.RawMessage `json:"payload"`
				Version     int64           `json:"version"`
			}
			if e := decode(w, r, &in); e != nil {
				return e
			}
			if len([]rune(strings.TrimSpace(in.Name))) < 1 || len([]rune(in.Name)) > 100 || len(in.Description) > 5000 || !(in.Status == "draft" || in.Status == "published" || in.Status == "archived") {
				return fail(400, "请检查名称、说明及状态")
			}
			if len(in.Payload) == 0 {
				in.Payload = json.RawMessage(`{}`)
			}
			var payload map[string]any
			if json.Unmarshal(in.Payload, &payload) != nil {
				return fail(400, "配置必须是 JSON 对象")
			}
			if len(parts) == 2 && payload["format"] == "duoyu-template" {
				return fail(400, "请通过来源用户创建快照模板")
			}
			if parts[1] == "widgets" && in.Status == "published" && payload["type"] != "calendar" && payload["type"] != "weather" && payload["type"] != "note" {
				return fail(400, "扩展支持 calendar、weather、note 组件")
			}
			if parts[1] == "wallpapers" && in.Status == "published" {
				url, _ := payload["url"].(string)
				hash := strings.TrimPrefix(url, s.cfg.Origin+"/catalog-assets/")
				var exists bool
				if !hashPattern.MatchString(hash) {
					return fail(400, "请先上传壁纸图片")
				}
				if e := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM backend_catalog_files WHERE sha256=$1)", hash).Scan(&exists); e != nil {
					return e
				}
				if !exists {
					return fail(400, "壁纸图片不存在")
				}
			}
			id := ""
			var e error
			if len(parts) == 2 {
				e = s.db.QueryRow(ctx, "INSERT INTO backend_catalog(kind,name,description,status,payload) VALUES($1,$2,$3,$4,$5) RETURNING id::text", parts[1], in.Name, in.Description, in.Status, in.Payload).Scan(&id)
			} else if len(parts) == 3 && uuidPattern.MatchString(parts[2]) {
				id = parts[2]
				var original []byte
				var isDefault bool
				if e = s.db.QueryRow(ctx, "SELECT payload,COALESCE((SELECT value#>>'{}' FROM backend_settings WHERE id='default_template_id')=id::text,false) FROM backend_catalog WHERE id=$1 AND kind=$2", id, parts[1]).Scan(&original, &isDefault); e != nil {
					return fail(404, "资源不存在")
				}
				if isDefault && in.Status != "published" {
					return fail(409, "请先取消官方默认模板，再归档或下架")
				}
				var source map[string]any
				json.Unmarshal(original, &source)
				if source["format"] != "duoyu-template" && payload["format"] == "duoyu-template" {
					return fail(400, "请通过来源用户创建快照模板")
				}
				if source["format"] == "duoyu-template" {
					in.Payload = original
				}
				tag, err := s.db.Exec(ctx, "UPDATE backend_catalog SET name=$3,description=$4,status=$5,payload=$6,version=version+1,updated_at=now() WHERE id=$1 AND kind=$2 AND version=$7", id, parts[1], in.Name, in.Description, in.Status, in.Payload, in.Version)
				e = err
				if e == nil && tag.RowsAffected() != 1 {
					return fail(409, "资源已被修改，请重新加载")
				}
			} else {
				return fail(400, "资源ID无效")
			}
			if e != nil {
				return e
			}
			s.audit(ctx, "catalog.save", id)
			writeJSON(w, 200, map[string]string{"id": id})
			return nil
		}
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "catalog/") {
		parts := strings.Split(path, "/")
		if len(parts) != 3 || !validKind(parts[1]) || !uuidPattern.MatchString(parts[2]) {
			return fail(400, "资源无效")
		}
		version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		var isDefault bool
		if e := s.db.QueryRow(ctx, "SELECT COALESCE((SELECT value#>>'{}' FROM backend_settings WHERE id='default_template_id')=$1,false)", parts[2]).Scan(&isDefault); e != nil {
			return e
		}
		if isDefault {
			return fail(409, "请先取消官方默认模板，再删除")
		}
		tag, e := s.db.Exec(ctx, "DELETE FROM backend_catalog WHERE id=$1 AND kind=$2 AND version=$3", parts[2], parts[1], version)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return fail(409, "资源已被修改，请重新加载")
		}
		s.audit(ctx, "catalog.delete", parts[2])
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	return fail(404, "接口不存在")
}
func validKind(k string) bool { return k == "templates" || k == "widgets" || k == "wallpapers" }
