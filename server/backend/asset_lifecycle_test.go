package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lifecycleImage(t *testing.T, s *Server, sub string) (Identity, string, []byte) {
	t.Helper()
	u := Identity{accountID(s.cfg.Issuer, sub), sub, sub, ""}
	if e := s.record(context.Background(), u); e != nil {
		t.Fatal(e)
	}
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF7sAAAAASUVORK5CYII=")
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	lifecycleUpload(t, s, u, hash, b)
	return u, hash, b
}
func lifecycleUpload(t *testing.T, s *Server, u Identity, hash string, b []byte) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/assets/"+hash, bytes.NewReader(b))
	r.Header.Set("Content-Type", "image/png")
	if e := s.assetRoute(httptest.NewRecorder(), r, u); e != nil {
		t.Fatal(e)
	}
}
func TestAssetLifecycleLeasesAndPhysicalDeletion(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	u, hash, b := lifecycleImage(t, s, "lifecycle")
	file := filepath.Join(s.cfg.AssetRoot, u.ID, hash)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(file); e != nil {
		t.Fatal("fresh upload collected")
	}
	s.db.Exec(ctx, "UPDATE sync_assets SET last_uploaded_at=now()-interval '25 hours' WHERE account_id=$1", u.ID)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("uncommitted zombie retained")
	}
	lifecycleUpload(t, s, u, hash, b)
	s.db.Exec(ctx, "INSERT INTO sync_entities VALUES($1,'preferences','image',$2,false,1)", u.ID, json.RawMessage(`{"id":"image","value":"duoyu-asset:`+hash+`"}`))
	s.db.Exec(ctx, "UPDATE sync_assets SET last_uploaded_at=now()-interval '25 hours' WHERE account_id=$1", u.ID)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(file); e != nil {
		t.Fatal("live asset collected")
	}
	s.db.Exec(ctx, "UPDATE sync_entities SET deleted=true WHERE account_id=$1", u.ID)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("released asset retained")
	}
	// A durable older unlink request cannot remove a re-upload of the same hash.
	lifecycleUpload(t, s, u, hash, b)
	s.db.Exec(ctx, "INSERT INTO sync_asset_deletions VALUES($1,$2,now())", u.ID, hash)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if actual, e := os.ReadFile(file); e != nil || !bytes.Equal(actual, b) {
		t.Fatal("reupload lost")
	}
	orphan := filepath.Join(s.cfg.AssetRoot, u.ID, strings.Repeat("e", 64))
	os.WriteFile(orphan, []byte("old"), 0600)
	old := time.Now().Add(-25 * time.Hour)
	os.Chtimes(orphan, old, old)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(orphan); !os.IsNotExist(e) {
		t.Fatal("filesystem orphan retained")
	}
}
func TestAdministratorAssetDeletionAndBackupCoordination(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	u, hash, _ := lifecycleImage(t, s, "delete")
	admin := loginCookie(t, s, "trusted-super", "admin")
	ordinary := loginCookie(t, s, "ordinary", "ordinary")
	ref := "duoyu-asset:" + hash
	for _, key := range []string{"a", "b"} {
		s.db.Exec(ctx, "INSERT INTO sync_entities VALUES($1,'libraryItems',$2,$3,false,1)", u.ID, key, json.RawMessage(`{"id":"`+key+`","icon":"duoyu-icon:{\"mode\":\"upload\",\"image\":\"`+ref+`\"}"}`))
	}
	s.db.Exec(ctx, "INSERT INTO sync_entities VALUES($1,'wallpapers','daily',$2,false,1)", u.ID, json.RawMessage(`{"id":"daily","asset":"`+ref+`"}`))
	s.db.Exec(ctx, "INSERT INTO sync_entities VALUES($1,'preferences','wallpaperMode',$2,false,1)", u.ID, json.RawMessage(`{"id":"wallpaperMode","value":"custom"}`))
	if r := callAdmin(s, admin, "GET", "users/"+u.ID+"/files", "", false); r.Code != 200 || !strings.Contains(r.Body.String(), `"reference_count":3`) {
		t.Fatal("file reference list failed", r.Code, r.Body.String())
	}
	path := "users/" + u.ID + "/files/" + hash
	if r := callAdmin(s, ordinary, "DELETE", path+"?force=true", "", true); r.Code != 403 {
		t.Fatal("ordinary deletion accepted", r.Code)
	}
	if r := callAdmin(s, admin, "DELETE", path, "", true); r.Code != 409 {
		t.Fatal("referenced deletion was not guarded", r.Code)
	}
	if r := callAdmin(s, admin, "DELETE", path+"?force=true", "", false); r.Code != 403 {
		t.Fatal("CSRF deletion accepted")
	}
	if r := callAdmin(s, admin, "DELETE", path+"?force=true", "", true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var changes []byte
	s.db.QueryRow(ctx, "SELECT changes FROM sync_changes WHERE account_id=$1 ORDER BY revision DESC LIMIT 1", u.ID).Scan(&changes)
	if strings.Contains(string(changes), ref) || !strings.Contains(string(changes), `"wallpaperMode"`) {
		t.Fatal("references not detached atomically", string(changes))
	}
	if r := callAdmin(s, admin, "DELETE", path, "", true); r.Code != 200 {
		t.Fatal("retry not idempotent")
	}
	file := filepath.Join(s.cfg.AssetRoot, u.ID, hash)
	lock, e := s.db.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Release()
	lock.Exec(ctx, "SELECT pg_advisory_lock(52615262)")
	if e = s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(file); e != nil {
		t.Fatal("collection raced backup")
	}
	lock.Exec(ctx, "SELECT pg_advisory_unlock(52615262)")
	if e = s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("physical deletion not completed")
	}
}
func TestAssetReferencesIgnoreInactiveIconsAndPreserveConflicts(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	u, hash, _ := lifecycleImage(t, s, "refs")
	ref := "duoyu-asset:" + hash
	s.db.Exec(ctx, "UPDATE sync_assets SET last_uploaded_at=now()-interval '25 hours' WHERE account_id=$1", u.ID)
	icon, _ := json.Marshal(map[string]any{"mode": "auto", "image": ref})
	payload, _ := json.Marshal(map[string]any{"icon": "duoyu-icon:" + string(icon)})
	s.db.Exec(ctx, "INSERT INTO sync_entities VALUES($1,'libraryItems','hidden',$2,false,1)", u.ID, payload)
	var count int
	s.db.QueryRow(ctx, "SELECT count(*) FROM sync_asset_references($1)", payload).Scan(&count)
	if count != 0 {
		t.Fatal("hidden image counted")
	}
	s.db.Exec(ctx, "INSERT INTO sync_conflicts(id,account_id,operation_id,detail) VALUES($1,$2,$3,$4)", newUUID(), u.ID, newUUID(), json.RawMessage(`{"value":"`+ref+`"}`))
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	var exists bool
	s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", u.ID, hash).Scan(&exists)
	if !exists {
		t.Fatal("unresolved conflict image collected")
	}
	s.db.Exec(ctx, "UPDATE sync_conflicts SET resolved_at=now() WHERE account_id=$1", u.ID)
	if e := s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sync_assets WHERE account_id=$1 AND sha256=$2)", u.ID, hash).Scan(&exists)
	if exists {
		t.Fatal("hidden zombie retained")
	}
}
