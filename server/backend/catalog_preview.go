package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var previewSlots = make(chan struct{}, 2)

func catalogPreview(ctx context.Context, b []byte) ([]byte, error) {
	select {
	case previewSlots <- struct{}{}:
		defer func() { <-previewSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	config, _, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, fail(415, "图片无法解码或像素超过4000万")
	}
	src, _, e := image.Decode(bytes.NewReader(b))
	if e != nil {
		return nil, fail(415, "图片无法解码，请更换图片")
	}
	w, h := config.Width, config.Height
	if w > 960 {
		h = max(1, h*960/w)
		w = 960
	}
	if h > 640 {
		w = max(1, w*640/h)
		h = 640
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if e = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 76}); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (s *Server) persistCatalogPreview(ctx context.Context, b []byte) (string, int, error) {
	thumb, e := catalogPreview(ctx, b)
	if e != nil {
		return "", 0, e
	}
	sum := sha256.Sum256(thumb)
	preview := hex.EncodeToString(sum[:])
	if e = s.persistFile("catalog", preview, thumb); e != nil {
		return "", 0, e
	}
	return preview, len(thumb), nil
}
func (s *Server) ensureCatalogPreview(ctx context.Context, hash, current string) (string, error) {
	if hashPattern.MatchString(current) {
		if st, e := os.Stat(filepath.Join(s.cfg.AssetRoot, "catalog", current)); e == nil && st.Mode().IsRegular() {
			return current, nil
		}
	}
	b, e := os.ReadFile(filepath.Join(s.cfg.AssetRoot, "catalog", hash))
	if e != nil {
		return "", fail(404, "原始图片不存在")
	}
	preview, n, e := s.persistCatalogPreview(ctx, b)
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, "UPDATE backend_catalog_files SET preview_sha256=$2,preview_bytes=$3 WHERE sha256=$1", hash, preview, n)
	return preview, e
}
