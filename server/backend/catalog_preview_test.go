package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogPreviewUploadAndAuthorization(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	src := image.NewNRGBA(image.Rect(0, 0, 1800, 1200))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(src.Pix); i += 4 {
		v := rng.Uint32()
		src.Pix[i] = byte(v)
		src.Pix[i+1] = byte(v >> 8)
		src.Pix[i+2] = byte(v >> 16)
		src.Pix[i+3] = 255
	}
	var original bytes.Buffer
	if e := png.Encode(&original, src); e != nil {
		t.Fatal(e)
	}
	request := httptest.NewRequest("POST", "/api/admin/catalog-assets", bytes.NewReader(original.Bytes()))
	request.AddCookie(admin)
	request.Header.Set("Origin", s.cfg.Origin)
	request.Header.Set("X-CSRF-Token", "csrf-test")
	request.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	s.handler(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if !strings.HasSuffix(result["preview_url"], "?preview=1") {
		t.Fatal(result)
	}
	hash := strings.TrimPrefix(result["url"], s.cfg.Origin+"/catalog-assets/")
	var preview string
	s.db.QueryRow(context.Background(), "SELECT preview_sha256 FROM backend_catalog_files WHERE sha256=$1", hash).Scan(&preview)
	thumbnail, e := os.ReadFile(filepath.Join(s.cfg.AssetRoot, "catalog", preview))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := jpeg.DecodeConfig(bytes.NewReader(thumbnail))
	if e != nil || cfg.Width != 960 || cfg.Height != 640 {
		t.Fatal(cfg, e)
	}
	if len(thumbnail) >= original.Len() {
		t.Fatal("preview not smaller")
	}
	stored, _ := os.ReadFile(filepath.Join(s.cfg.AssetRoot, "catalog", hash))
	if !bytes.Equal(stored, original.Bytes()) {
		t.Fatal("original modified")
	}
	path := "/catalog-assets/" + hash + "?preview=1"
	get := func(cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if cookie {
			r.AddCookie(admin)
		}
		w := httptest.NewRecorder()
		s.handler(w, r)
		return w
	}
	if r := get(false); r.Code != 404 {
		t.Fatal("unpublished preview exposed", r.Code)
	}
	if r := get(true); r.Code != 200 || r.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("admin preview failed", r.Code, r.Body.String())
	}
	_, e = s.db.Exec(context.Background(), "INSERT INTO backend_catalog(kind,name,status,payload) VALUES('wallpapers','test','published',jsonb_build_object('url',$1::text))", result["url"])
	if e != nil {
		t.Fatal(e)
	}
	tokenResponse, err := http.Get(s.cfg.Issuer + "/test-issue")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := io.ReadAll(tokenResponse.Body)
	tokenResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	catalogRequest := httptest.NewRequest("GET", "/api/v1/catalog/wallpapers", nil)
	catalogRequest.Header.Set("Authorization", "Bearer ordinary")
	catalogRequest.Header.Set("X-Duoyu-Identity", string(identity))
	catalogResponse := httptest.NewRecorder()
	s.handler(catalogResponse, catalogRequest)
	var listing struct {
		Items []struct {
			Payload map[string]string `json:"payload"`
		} `json:"items"`
	}
	if catalogResponse.Code != 200 {
		t.Fatal(catalogResponse.Code, catalogResponse.Body.String())
	}
	if err = json.Unmarshal(catalogResponse.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.Items[0].Payload["url"] != result["url"] || listing.Items[0].Payload["preview_url"] != result["url"]+"?preview=1" {
		t.Fatal("original and preview API fields incorrect", catalogResponse.Body.String())
	}
	if r := get(false); r.Code != 200 || r.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("public compressed preview failed", r.Code)
	}
	s.db.Exec(context.Background(), "UPDATE backend_catalog_files SET preview_sha256=NULL,preview_bytes=NULL WHERE sha256=$1", hash)
	if r := get(false); r.Code != 200 {
		t.Fatal("existing wallpaper preview regeneration failed", r.Code)
	}
}
func TestCatalogPreviewRejectsInvalidImage(t *testing.T) {
	if _, e := catalogPreview(context.Background(), []byte("invalid")); e == nil {
		t.Fatal("invalid image accepted")
	}
}
