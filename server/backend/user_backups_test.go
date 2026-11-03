package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserBackupsRetentionAndFiles(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	admin := loginCookie(t, s, "trusted-super", "admin")
	ordinary := loginCookie(t, s, "ordinary", "ordinary")
	u := Identity{accountID(s.cfg.Issuer, "backup-user"), "backup-user", "备份用户", ""}
	if e := s.record(ctx, u); e != nil {
		t.Fatal(e)
	}
	hash := strings.Repeat("a", 64)
	image := []byte("retained-image")
	if e := os.MkdirAll(filepath.Join(s.cfg.AssetRoot, u.ID), 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(s.cfg.AssetRoot, u.ID, hash)
	if e := os.WriteFile(file, image, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(ctx, "INSERT INTO sync_assets(account_id,sha256,media_type,body,byte_size,file_path) VALUES($1,$2,'image/png',NULL,$3,$4)", u.ID, hash, len(image), u.ID+"/"+hash); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(ctx, `INSERT INTO sync_entities VALUES($1,'wallpapers','wallpaper',jsonb_build_object('image',$2::text),false,1)`, u.ID, "duoyu-asset:"+hash); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 7; i++ {
		if _, e := s.db.Exec(ctx, "INSERT INTO sync_changes(account_id,revision,operation_id,changes) VALUES($1,$2,gen_random_uuid(),'[]')", u.ID, i); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	var minimum int
	if e := s.db.QueryRow(ctx, "SELECT count(*),min(revision) FROM sync_user_backups WHERE account_id=$1", u.ID).Scan(&count, &minimum); e != nil || count != 5 || minimum != 3 {
		t.Fatal("retention", count, minimum, e)
	}
	if _, e := s.db.Exec(ctx, "SELECT sync_capture_user_backup($1,7)", u.ID); e != nil {
		t.Fatal(e)
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tx.Exec(ctx, "SELECT sync_capture_user_backup($1,8)", u.ID)
	tx.Rollback(ctx)
	var backupID string
	s.db.QueryRow(ctx, "SELECT id::text FROM sync_user_backups WHERE account_id=$1 ORDER BY revision DESC LIMIT 1", u.ID).Scan(&backupID)
	r := callAdmin(s, admin, "GET", "backups/users/"+u.ID+"/"+backupID, "", false)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	archive, e := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range archive.File {
		if f.Name == "assets/"+hash {
			stream, _ := f.Open()
			b, _ := io.ReadAll(stream)
			stream.Close()
			if !bytes.Equal(b, image) {
				t.Fatal("image mismatch")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("backup lost image")
	}
	r = callAdmin(s, ordinary, "GET", "backups/users/"+u.ID, "", false)
	if r.Code != 403 {
		t.Fatal("ordinary user read backups")
	}
	s.db.Exec(ctx, "DELETE FROM sync_assets WHERE account_id=$1", u.ID)
	s.db.Exec(ctx, "INSERT INTO sync_asset_deletions(account_id,sha256) VALUES($1,$2)", u.ID, hash)
	old := time.Now().Add(-25 * time.Hour)
	os.Chtimes(file, old, old)
	if e = s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(file); e != nil {
		t.Fatal("retained backup lost binary", e)
	}
	s.db.Exec(ctx, "DELETE FROM sync_entities WHERE account_id=$1", u.ID)
	for i := 8; i <= 12; i++ {
		s.db.Exec(ctx, "SELECT sync_capture_user_backup($1,$2)", u.ID, i)
	}
	if e = s.cleanupAssets(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("expired backup retained orphan binary")
	}
	r = callAdmin(s, admin, "GET", "backups/users/"+u.ID+"/"+backupID, "", false)
	if r.Code != 404 {
		t.Fatal("rotated backup downloadable")
	}
}
func TestSystemBackupListAndDownload(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	id := "11111111-1111-4111-8111-111111111111"
	object := id + "/" + strings.Repeat("b", 64) + ".enc"
	os.MkdirAll(filepath.Join(s.cfg.BackupDir, "objects", id), 0700)
	os.WriteFile(filepath.Join(s.cfg.BackupDir, "objects", object), []byte("encrypted-test"), 0600)
	record := systemBackup{ID: id, Kind: "incremental", CreatedAt: time.Now(), Bytes: 100, AddedBytes: 20, Files: []backupFile{{Path: "database.dump", Parts: []backupPart{{Object: object, Bytes: 100}}}}}
	raw, _ := json.Marshal(record)
	os.WriteFile(filepath.Join(s.cfg.BackupDir, id+".record.json"), raw, 0600)
	r := callAdmin(s, admin, "GET", "backups/system", "", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "incremental") {
		t.Fatal(r.Code, r.Body.String())
	}
	r = callAdmin(s, admin, "GET", "backups/system/"+id, "", false)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	z, e := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
	if e != nil || len(z.File) != 2 {
		t.Fatal("system bundle incomplete", e)
	}
	os.Remove(filepath.Join(s.cfg.BackupDir, "objects", object))
	r = callAdmin(s, admin, "GET", "backups/system/"+id, "", false)
	if r.Code != 503 {
		t.Fatal("missing object allowed download")
	}
}
