package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type templateEntity struct {
	Table   string         `json:"table"`
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
}
type templateAsset struct {
	Hash  string `json:"sha256"`
	Media string `json:"media_type"`
	Bytes int64  `json:"bytes"`
}
type templateSnapshot struct {
	Format   string           `json:"format"`
	Version  int              `json:"version"`
	Source   string           `json:"source_account_id"`
	Entities []templateEntity `json:"entities"`
	Assets   []templateAsset  `json:"assets"`
}

var assetReference = regexp.MustCompile(`duoyu-asset:([a-f0-9]{64})`)

func newUUID() string {
	h := digest(randomToken())
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}
func (s *Server) captureTemplate(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Account     string `json:"account_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !uuidPattern.MatchString(in.Account) || len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 100 || len(in.Description) > 5000 {
		return fail(400, "请选择有效用户并填写模板名称")
	}
	snap := templateSnapshot{Format: "duoyu-template", Version: 1, Source: in.Account, Entities: []templateEntity{}, Assets: []templateAsset{}}
	e := s.tx(r.Context(), in.Account, func(tx pgx.Tx) error {
		// Same row lock as sync writes: the snapshot is one coherent account revision.
		if _, e := tx.Exec(r.Context(), "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", in.Account); e != nil {
			return e
		}
		rows, e := tx.Query(r.Context(), "SELECT entity_type,entity_id,payload FROM sync_entities WHERE account_id=$1 AND NOT deleted ORDER BY entity_type,entity_id", in.Account)
		if e != nil {
			return e
		}
		for rows.Next() {
			var item templateEntity
			if e = rows.Scan(&item.Table, &item.ID, &item.Payload); e != nil {
				rows.Close()
				return e
			}
			item.Payload = normalizeIconUploads(item.Payload).(map[string]any)
			snap.Entities = append(snap.Entities, item)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if len(snap.Entities) == 0 {
			return fail(400, "该用户没有可作为模板的数据")
		}
		if len(snap.Entities) > 2000 {
			return fail(413, "模板最多包含2000个数据项")
		}
		data, _ := json.Marshal(snap.Entities)
		refs := assetReference.FindAllSubmatch(data, -1)
		seen := map[string]bool{}
		var used int64
		for _, ref := range refs {
			hash := string(ref[1])
			if seen[hash] {
				continue
			}
			seen[hash] = true
			var b []byte
			var path *string
			var media string
			e := tx.QueryRow(r.Context(), "SELECT media_type,body,file_path FROM sync_assets WHERE account_id=$1 AND sha256=$2", in.Account, hash).Scan(&media, &b, &path)
			if e != nil {
				return fail(409, "用户图片尚未上传完整，请先完成同步")
			}
			if path != nil {
				if *path != s.relativeFile(in.Account, hash) {
					return fail(409, "图片路径异常")
				}
				b, e = os.ReadFile(filepath.Join(s.cfg.AssetRoot, *path))
				if e != nil {
					return e
				}
			}
			if digest(string(b)) != hash {
				return fail(409, "图片校验失败")
			}
			if e = s.persistFile("catalog", hash, b); e != nil {
				return e
			}
			snap.Assets = append(snap.Assets, templateAsset{hash, media, int64(len(b))})
			used += int64(len(b))
		}
		var quota int64
		if e := tx.QueryRow(r.Context(), "SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes'").Scan(&quota); e != nil {
			return e
		}
		if used > quota {
			return fail(413, "模板图片超过当前默认用户配额")
		}
		return nil
	})
	if e != nil {
		return e
	}
	data, _ := json.Marshal(snap)
	var id string
	e = s.db.QueryRow(r.Context(), "INSERT INTO backend_catalog(kind,name,status,payload,description) VALUES('templates',$1,'draft',$2,$3) RETURNING id::text", in.Name, data, in.Description).Scan(&id)
	if e != nil {
		return e
	}
	s.audit(r.Context(), "template.capture", id)
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (s *Server) setDefaultTemplate(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.ID != "" {
		if !uuidPattern.MatchString(in.ID) {
			return fail(400, "模板ID无效")
		}
		var valid bool
		if e := s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM backend_catalog WHERE id=$1 AND kind='templates' AND status='published' AND payload->>'format'='duoyu-template')", in.ID).Scan(&valid); e != nil || !valid {
			return fail(400, "请先发布从用户数据创建的模板")
		}
		var required, quota int64
		if e := s.db.QueryRow(r.Context(), "SELECT COALESCE(sum((a->>'bytes')::bigint),0),(SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes') FROM backend_catalog c CROSS JOIN LATERAL jsonb_array_elements(c.payload->'assets') a WHERE c.id=$1", in.ID).Scan(&required, &quota); e != nil {
			return e
		}
		if required > quota {
			return fail(409, "模板图片超过默认用户配额，请先提高配额")
		}
	}
	data, _ := json.Marshal(in.ID)
	_, e := s.db.Exec(r.Context(), "INSERT INTO backend_settings(id,value) VALUES('default_template_id',$1) ON CONFLICT(id) DO UPDATE SET value=excluded.value", data)
	if e != nil {
		return e
	}
	s.audit(r.Context(), "template.default", in.ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (s *Server) initializeAccount(ctx context.Context, u Identity) error {
	return s.tx(ctx, u.ID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", u.ID); e != nil {
			return e
		}
		var initialized, hasData bool
		e := tx.QueryRow(ctx, "SELECT initialized,EXISTS(SELECT 1 FROM sync_entities WHERE account_id=$1) FROM backend_users WHERE account_id=$1", u.ID).Scan(&initialized, &hasData)
		if e != nil || initialized {
			return e
		}
		if !hasData {
			var id string
			e = tx.QueryRow(ctx, "SELECT value#>>'{}' FROM backend_settings WHERE id='default_template_id'").Scan(&id)
			if e != nil && e != pgx.ErrNoRows {
				return e
			}
			if id != "" {
				if _, e = s.applyTemplateTx(ctx, tx, u.ID, id, newUUID(), true); e != nil {
					return e
				}
			}
		}
		_, e = tx.Exec(ctx, "UPDATE backend_users SET initialized=true WHERE account_id=$1", u.ID)
		return e
	})
}
func (s *Server) applyTemplate(w http.ResponseWriter, r *http.Request, u Identity, id string) error {
	if r.Method != "POST" || !uuidPattern.MatchString(id) {
		return fail(400, "模板无效")
	}
	var in struct {
		RequestID string `json:"request_id"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !uuidPattern.MatchString(in.RequestID) {
		return fail(400, "请求ID无效")
	}
	var revision int64
	e := s.tx(r.Context(), u.ID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(r.Context(), "SELECT id FROM sync_accounts WHERE id=$1 FOR UPDATE", u.ID); e != nil {
			return e
		}
		var previousTemplate string
		err := tx.QueryRow(r.Context(), "SELECT template_id::text,revision FROM backend_template_applies WHERE account_id=$1 AND request_id=$2", u.ID, in.RequestID).Scan(&previousTemplate, &revision)
		if err == nil {
			if previousTemplate != id {
				return fail(409, "请求ID已经用于其他模板")
			}
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		var e error
		revision, e = s.applyTemplateTx(r.Context(), tx, u.ID, id, in.RequestID, false)
		return e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"revision": strconv.FormatInt(revision, 10)})
	return nil
}
func (s *Server) applyTemplateTx(ctx context.Context, tx pgx.Tx, account, template, request string, initial bool) (int64, error) {
	var data []byte
	var name string
	e := tx.QueryRow(ctx, "SELECT payload,name FROM backend_catalog WHERE id=$1 AND kind='templates' AND status='published' FOR SHARE", template).Scan(&data, &name)
	if e != nil {
		return 0, fail(409, "官方模板不可用，请联系管理员")
	}
	var snap templateSnapshot
	if json.Unmarshal(data, &snap) != nil || snap.Format != "duoyu-template" || snap.Version != 1 {
		return 0, fail(400, "模板格式无效")
	}
	maps := map[string]string{}
	for _, entity := range snap.Entities {
		maps[entity.Table+":"+entity.ID] = newUUID()
	}
	if initial {
		for _, entity := range snap.Entities {
			if entity.Table == "preferences" || entity.Table == "shortcuts" || entity.Table == "wallpapers" {
				maps[entity.Table+":"+entity.ID] = entity.ID
			}
		}
	}
	updates := []map[string]any{}
	mapPage := func(old string) string {
		if id := maps["libraryPages:"+old]; id != "" {
			return id
		}
		if initial && (old == "home" || old == "__dock") {
			return old
		}
		id := newUUID()
		maps["libraryPages:"+old] = id
		updates = append(updates, map[string]any{"table": "libraryPages", "id": id, "payload": map[string]any{"id": id, "name": name, "order": 0}, "deleted": false})
		return id
	}
	for _, entity := range snap.Entities {
		if !initial && (entity.Table == "preferences" || entity.Table == "shortcuts" || entity.Table == "shortcutItems" || entity.Table == "wallpapers") {
			continue
		}
		p := normalizeIconUploads(entity.Payload).(map[string]any)
		id := maps[entity.Table+":"+entity.ID]
		p["id"] = id
		switch entity.Table {
		case "libraryItems":
			p["pageId"] = mapPage(fmt.Sprint(p["pageId"]))
			if parent, ok := p["parent"].(string); ok && parent != "" {
				p["parent"] = maps["libraryItems:"+parent]
			}
		case "tabCollections":
			if old, ok := p["spaceId"].(string); ok {
				p["spaceId"] = maps["workspaceSpaces:"+old]
			}
		case "savedTabs":
			p["collectionId"] = maps["tabCollections:"+fmt.Sprint(p["collectionId"])]
		case "shortcutItems":
			group := fmt.Sprint(p["groupId"])
			p["groupId"] = maps["shortcuts:"+group]
			id = fmt.Sprint(p["groupId"]) + ":" + newUUID()
			p["id"] = id
		case "preferences":
			if entity.ID == "dockOrder" {
				if values, ok := p["value"].([]any); ok {
					for i, value := range values {
						if old, ok := value.(string); ok && maps["libraryItems:"+old] != "" {
							values[i] = maps["libraryItems:"+old]
						}
					}
				}
			}
			if len(entity.ID) > 7 && entity.ID[:7] == "layout:" {
				id = "layout:" + mapPage(entity.ID[7:])
				p["id"] = id
			}
		}
		updates = append(updates, map[string]any{"table": entity.Table, "id": id, "payload": p, "deleted": false})
	}
	bytes, _ := json.Marshal(updates)
	if len(bytes) > 8<<20 {
		return 0, fail(413, "模板数据过大")
	}
	var entityCount int64
	var currentBytes int64
	if e = tx.QueryRow(ctx, "SELECT COALESCE(sum(octet_length(payload::text)),0) FROM sync_entities WHERE account_id=$1", account).Scan(&currentBytes); e != nil {
		return 0, e
	}
	if currentBytes+int64(len(bytes)) > 8<<20 {
		return 0, fail(413, "账号数据容量已满")
	}
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM sync_entities WHERE account_id=$1", account).Scan(&entityCount); e != nil {
		return 0, e
	}
	if entityCount+int64(len(updates)) > 10000 {
		return 0, fail(413, "账号数据容量已满")
	}
	refs := assetReference.FindAllSubmatch(bytes, -1)
	needed := map[string]bool{}
	for _, ref := range refs {
		needed[string(ref[1])] = true
	}
	var used int64
	if e = tx.QueryRow(ctx, "SELECT COALESCE(sum(byte_size),0) FROM sync_assets WHERE account_id=$1", account).Scan(&used); e != nil {
		return 0, e
	}
	quota, e := s.quota(ctx, tx, account)
	if e != nil {
		return 0, e
	}
	for _, asset := range snap.Assets {
		if !hashPattern.MatchString(asset.Hash) {
			return 0, fail(409, "模板图片标识无效")
		}
		if !needed[asset.Hash] {
			continue
		}
		var exists bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", account, asset.Hash).Scan(&exists); e != nil {
			return 0, e
		}
		if exists {
			delete(needed, asset.Hash)
			continue
		}
		b, e := os.ReadFile(filepath.Join(s.cfg.AssetRoot, "catalog", asset.Hash))
		if e != nil {
			return 0, e
		}
		if !hashPattern.MatchString(asset.Hash) || digest(string(b)) != asset.Hash {
			return 0, fail(409, "模板图片损坏")
		}
		used += int64(len(b))
		if used > quota {
			return 0, fail(413, "模板图片超过用户配额")
		}
		if e = s.persistFile(account, asset.Hash, b); e != nil {
			return 0, e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO sync_assets(account_id,sha256,media_type,body,byte_size,file_path) VALUES($1,$2,$3,NULL,$4,$5)", account, asset.Hash, asset.Media, len(b), s.relativeFile(account, asset.Hash)); e != nil {
			return 0, e
		}
		delete(needed, asset.Hash)
	}
	if len(needed) > 0 {
		return 0, fail(409, "模板缺少图片")
	}
	var revision int64
	if e = tx.QueryRow(ctx, "UPDATE sync_accounts SET head_revision=head_revision+1 WHERE id=$1 RETURNING head_revision", account).Scan(&revision); e != nil {
		return 0, e
	}
	for _, update := range updates {
		update["revision"] = strconv.FormatInt(revision, 10)
		p, _ := json.Marshal(update["payload"])
		if _, e = tx.Exec(ctx, "INSERT INTO sync_entities(account_id,entity_type,entity_id,payload,deleted,revision) VALUES($1,$2,$3,$4,false,$5)", account, update["table"], update["id"], p, revision); e != nil {
			return 0, e
		}
	}
	bytes, _ = json.Marshal(updates)
	if _, e = tx.Exec(ctx, "INSERT INTO sync_changes(account_id,revision,operation_id,changes) VALUES($1,$2,$3,$4)", account, revision, request, bytes); e != nil {
		return 0, e
	}
	_, e = tx.Exec(ctx, "INSERT INTO backend_template_applies(account_id,request_id,template_id,revision) VALUES($1,$2,$3,$4)", account, request, template, revision)
	return revision, e
}

func normalizeIconUploads(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > 11 && x[:11] == "duoyu-icon:" {
			var icon map[string]any
			if json.Unmarshal([]byte(x[11:]), &icon) == nil {
				if icon["mode"] != "upload" {
					icon["image"] = ""
				}
				b, _ := json.Marshal(icon)
				return "duoyu-icon:" + string(b)
			}
		}
	case []any:
		for i, a := range x {
			x[i] = normalizeIconUploads(a)
		}
	case map[string]any:
		for k, a := range x {
			x[k] = normalizeIconUploads(a)
		}
	}
	return v
}
