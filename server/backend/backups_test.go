package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupManagementListingAndDownload(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	ordinary := loginCookie(t, s, "ordinary", "ordinary")
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("sync-2026-10-%02dT00-00-00.000Z.dump.enc", i+1)
		file := filepath.Join(s.cfg.BackupDir, name)
		if e := os.WriteFile(file, []byte("DYS1encrypted-placeholder"), 0600); e != nil {
			t.Fatal(e)
		}
		stamp := time.Now().Add(time.Duration(i) * time.Minute)
		os.Chtimes(file, stamp, stamp)
	}
	os.WriteFile(filepath.Join(s.cfg.BackupDir, "private.key"), []byte("not a backup"), 0600)
	directoryName := "sync-2026-09-01T00-00-00.000Z.dump.enc"
	os.Mkdir(filepath.Join(s.cfg.BackupDir, directoryName), 0700)
	linkName := "sync-2026-09-02T00-00-00.000Z.dump.enc"
	symlink := os.Symlink(filepath.Join(s.cfg.BackupDir, "private.key"), filepath.Join(s.cfg.BackupDir, linkName)) == nil
	list := func(path string) []map[string]any {
		r := callAdmin(s, admin, "GET", path, "", false)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var data struct {
			Items []map[string]any `json:"items"`
		}
		if e := json.Unmarshal(r.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		return data.Items
	}
	first := list("backups")
	if len(first) != 20 || first[0]["total"] != float64(24) || first[0]["name"] != "sync-2026-10-24T00-00-00.000Z.dump.enc" {
		t.Fatal("list filtering/pagination failed", first)
	}
	if len(list("backups?page=2")) != 4 || len(list("backups?page=999")) != 0 {
		t.Fatal("pagination failed")
	}
	if r := callAdmin(s, ordinary, "GET", "backups", "", false); r.Code != http.StatusForbidden {
		t.Fatal("ordinary user listed backups")
	}
	name := first[0]["name"].(string)
	r := callAdmin(s, admin, "GET", "backups/"+name, "", false)
	if r.Code != 200 || r.Body.String() != "DYS1encrypted-placeholder" || r.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(r.Header().Get("Content-Disposition"), name) {
		t.Fatal("download failed", r.Code, r.Body.String())
	}
	var audits int
	s.db.QueryRow(context.Background(), "SELECT count(*) FROM backend_audit WHERE action='backup.download'").Scan(&audits)
	if audits != 1 {
		t.Fatal("download not audited")
	}
	for _, path := range []string{"backups/private.key", "backups/" + directoryName, "backups/sync-2025-01-01T00-00-00.000Z.dump.enc"} {
		if r := callAdmin(s, admin, "GET", path, "", false); r.Code != 400 && r.Code != 404 {
			t.Fatal("invalid backup exposed", r.Code)
		}
	}
	if symlink {
		if r := callAdmin(s, admin, "GET", "backups/"+linkName, "", false); r.Code != 404 {
			t.Fatal("backup symlink followed")
		}
	}
	s.cfg.BackupDir = filepath.Join(s.cfg.BackupDir, "private.key")
	r = callAdmin(s, admin, "GET", "backups", "", false)
	if r.Code != 503 || !strings.Contains(r.Body.String(), "目录访问权限") {
		t.Fatal("backup diagnostic missing", r.Code, r.Body.String())
	}
}
