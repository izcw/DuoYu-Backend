package main

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type siteRoundTrip func(*http.Request) (*http.Response, error)

func (f siteRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSiteMetadataValidation(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "192.0.2.1"} {
		if publicSiteIP(netip.MustParseAddr(raw)) {
			t.Fatal("allowed private/special IP", raw)
		}
	}
	if !publicSiteIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("rejected public IP")
	}
	for _, raw := range []string{"file:///etc/passwd", "https://a:b@example.com/", "https://example.com:22/"} {
		if _, e := validSiteURL(raw); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	title, icons := parseSite(`<title>A &amp; B</title><link rel='shortcut icon' href='../logo.png'><link rel=icon href=javascript:bad>`, "https://example.com/en/")
	if title != "A & B" || len(icons) != 1 || icons[0] != "https://example.com/logo.png" {
		t.Fatalf("unexpected result %s %v", title, icons)
	}
}
func TestSiteMetadataFallback(t *testing.T) {
	old := siteHTTP
	defer func() { siteHTTP = old }()
	var requests []string
	siteHTTP = &http.Client{Transport: siteRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("credentials forwarded")
		}
		status := 503
		body := "unavailable"
		media := "text/plain"
		if r.URL.Host == "icons.duckduckgo.com" {
			status = 200
			body = string([]byte{0, 0, 1, 0, 1, 0})
			media = "image/x-icon"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	u, _ := url.Parse("https://example.com/path?private=query")
	result := resolveSite(context.Background(), u)
	if result.Icon == "" || result.Source != "public-icon-cache" {
		t.Fatalf("no fallback: %+v", result)
	}
	if requests[len(requests)-1] != "https://icons.duckduckgo.com/ip3/example.com.ico" {
		t.Fatal("full URL leaked to public cache")
	}
}
func TestSiteMetadataBlocksLoopback(t *testing.T) {
	_, _, _, e := readSite(context.Background(), "http://127.0.0.1/")
	if e == nil {
		t.Fatal("loopback request allowed")
	}
}

func TestSiteIconMedia(t *testing.T) {
	wrap := func(shape string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">` + shape + `</svg>`
	}
	for _, markup := range []string{
		wrap(`<rect width="32" height="32" fill="#fff"/><path d="M0 0L32 32Z"/>`),
		wrap(`<g><circle cx="5" cy="5" r="4" stroke="black"/></g>`),
	} {
		if siteIconType([]byte(markup), "image/svg+xml; charset=UTF-8") != "image/svg+xml" {
			t.Fatal("rejected static geometry")
		}
	}
	for _, markup := range []string{
		`<html onload="alert(1)"/>`, wrap(`<script>alert(1)</script>`), wrap(`<image href="https://example.com/a"/>`),
		wrap(`<path onclick="x()" d="M0 0"/>`), wrap(`<path fill="url(https://example.com/x)" d="M0 0"/>`),
		wrap(`<rect style="fill:red"/>`), wrap(`<foreignObject><iframe/></foreignObject>`), wrap(`<path>`),
		`<?xml-stylesheet href="https://example.com"?>` + wrap(`<rect/>`), `<!DOCTYPE svg>` + wrap(`<rect/>`),
		wrap(`<rect/>`) + wrap(`<path/>`), strings.Repeat(" ", 65536) + wrap(`<rect/>`),
	} {
		if siteIconType([]byte(markup), "image/svg+xml") != "" {
			t.Fatal("accepted invalid or active SVG")
		}
	}
	if siteIconType([]byte{0, 0, 1, 0, 1, 0}, "image/x-icon") != "image/x-icon" {
		t.Fatal("lost ICO compatibility")
	}
	if siteIconType([]byte("\x89PNG\r\n\x1a\n"), "image/svg+xml") != "image/png" {
		t.Fatal("trusted declared type over raster signature")
	}
}
