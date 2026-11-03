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
