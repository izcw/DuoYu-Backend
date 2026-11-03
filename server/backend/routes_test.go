package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestManagementRootAndRemovedRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("management-root"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{AdminDist: dir}}
	for _, path := range []string{"/", "/users", "/users/example/settings"} {
		w := httptest.NewRecorder()
		s.handler(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != "management-root" {
			t.Fatalf("management route %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/admin/", "/v1/sync/snapshot", "/api/missing"} {
		w := httptest.NewRecorder()
		s.handler(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("removed/unknown API route %s: %d", path, w.Code)
		}
	}
}
