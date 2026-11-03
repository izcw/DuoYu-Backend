package main

import (
	"context"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type siteMetadata struct {
	Title  string `json:"title"`
	Icon   string `json:"icon"`
	Source string `json:"source"`
}
type siteCacheEntry struct {
	value siteMetadata
	until time.Time
}

var siteCache = struct {
	sync.Mutex
	entries map[string]siteCacheEntry
}{entries: make(map[string]siteCacheEntry)}
var siteSlots = make(chan struct{}, 8)
var siteRate = struct {
	sync.Mutex
	entries map[string]struct {
		count int
		until time.Time
	}
}{entries: make(map[string]struct {
	count int
	until time.Time
})}
var siteTitlePattern = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
var siteTagPattern = regexp.MustCompile(`(?is)<(?:link|meta)\b[^>]*>`)
var siteAttrPattern = regexp.MustCompile(`(?is)([\w:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

func publicSiteIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Use public IPv4 only, excluding special-use networks and IPv6 transition ranges.
	if !ip.Is4() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}
func validSiteURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u == nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return nil, errors.New("invalid website URL")
	}
	u.Fragment = ""
	return u, nil
}

var siteHTTP = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, e
			}
			ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			if e != nil {
				return nil, e
			}
			if len(ips) == 0 {
				return nil, errors.New("no public address")
			}
			for _, ip := range ips {
				if !publicSiteIP(ip) {
					return nil, errors.New("non-public address")
				}
			}
			// Dial the validated address directly: DNS cannot change between validation and connection.
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].String(), port))
		},
	},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		_, e := validSiteURL(r.URL.String())
		return e
	},
}

func readSite(ctx context.Context, raw string) ([]byte, string, string, error) {
	if _, e := validSiteURL(raw); e != nil {
		return nil, "", "", e
	}
	r, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, "", "", e
	}
	r.Header.Set("User-Agent", "DuoYu-Metadata/1.0")
	response, e := siteHTTP.Do(r)
	if e != nil {
		return nil, "", "", e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", "", errors.New("website unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if e != nil || len(b) > 1048576 {
		return nil, "", "", errors.New("response too large")
	}
	return b, response.Header.Get("Content-Type"), response.Request.URL.String(), nil
}
func parseSite(htmlText, base string) (string, []string) {
	title := ""
	icons := []string{}
	if m := siteTitlePattern.FindStringSubmatch(htmlText); len(m) > 1 {
		title = html.UnescapeString(strings.TrimSpace(m[1]))
	}
	u, _ := url.Parse(base)
	for _, tag := range siteTagPattern.FindAllString(htmlText, -1) {
		attrs := map[string]string{}
		for _, m := range siteAttrPattern.FindAllStringSubmatch(tag, -1) {
			v := m[2]
			if v == "" {
				v = m[3]
			}
			if v == "" {
				v = m[4]
			}
			attrs[strings.ToLower(m[1])] = html.UnescapeString(v)
		}
		if title == "" && (attrs["property"] == "og:title" || attrs["name"] == "twitter:title") {
			title = attrs["content"]
		}
		icon := false
		for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
			if rel == "icon" || rel == "apple-touch-icon" {
				icon = true
			}
		}
		if icon && attrs["href"] != "" {
			if ref, e := url.Parse(attrs["href"]); e == nil && u != nil {
				candidate := u.ResolveReference(ref).String()
				if _, e = validSiteURL(candidate); e == nil {
					icons = append(icons, candidate)
				}
			}
		}
	}
	if len([]rune(title)) > 200 {
		title = string([]rune(title)[:200])
	}
	if len(icons) > 4 {
		icons = icons[:4]
	}
	return title, icons
}
func resolveSite(ctx context.Context, u *url.URL) siteMetadata {
	originContext, cancelOrigin := context.WithTimeout(ctx, 9*time.Second)
	defer cancelOrigin()
	value := siteMetadata{Source: "website"}
	icons := []string{}
	if b, _, final, e := readSite(originContext, u.String()); e == nil {
		value.Title, icons = parseSite(string(b), final)
	}
	icons = append(icons, u.Scheme+"://"+u.Host+"/favicon.ico")
	// Only the hostname is sent to the public icon cache; never paths, queries or credentials.
	icons = append(icons, "https://icons.duckduckgo.com/ip3/"+url.PathEscape(u.Hostname())+".ico")
	for i, candidate := range icons {
		requestContext := originContext
		if i == len(icons)-1 {
			requestContext = ctx
		}
		b, media, _, e := readSite(requestContext, candidate)
		detected := http.DetectContentType(b)
		if e != nil || len(b) == 0 || !strings.HasPrefix(media, "image/") || !(strings.HasPrefix(detected, "image/") || (len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 1 && b[3] == 0)) {
			continue
		}
		media = strings.Split(media, ";")[0]
		value.Icon = "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(b)
		if i == len(icons)-1 {
			value.Source = "public-icon-cache"
		}
		break
	}
	return value
}
func (s *Server) websiteMetadata(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		return fail(405, "仅支持 GET")
	}
	u, e := validSiteURL(r.URL.Query().Get("url"))
	if e != nil {
		return fail(400, "网址格式无效")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !publicSiteIP(ip) {
		return fail(400, "不支持内网网址")
	}
	// Do not trust client-supplied forwarding headers for this public endpoint.
	client, _, _ := net.SplitHostPort(r.RemoteAddr)
	siteRate.Lock()
	now := time.Now()
	for key, value := range siteRate.entries {
		if now.After(value.until) {
			delete(siteRate.entries, key)
		}
	}
	rate := siteRate.entries[client]
	if rate.count == 0 {
		rate.until = now.Add(time.Minute)
	}
	rate.count++
	limited := rate.count > 120 || len(siteRate.entries) >= 2000
	if !limited {
		siteRate.entries[client] = rate
	}
	siteRate.Unlock()
	if limited {
		return fail(429, "请求过于频繁，请稍后重试")
	}
	key := u.String()
	siteCache.Lock()
	cached, ok := siteCache.entries[key]
	siteCache.Unlock()
	if ok && time.Now().Before(cached.until) {
		writeJSON(w, 200, cached.value)
		return nil
	}
	select {
	case siteSlots <- struct{}{}:
		defer func() { <-siteSlots }()
	default:
		return fail(429, "解析繁忙，请稍后重试")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	value := resolveSite(ctx, u)
	if ok {
		if value.Title == "" {
			value.Title = cached.value.Title
		}
		if value.Icon == "" {
			value.Icon = cached.value.Icon
			value.Source = "stale-cache"
		}
	}
	ttl := time.Minute
	if value.Icon != "" || value.Title != "" {
		ttl = time.Hour
	}
	siteCache.Lock()
	if len(siteCache.entries) >= 128 {
		for k := range siteCache.entries {
			delete(siteCache.entries, k)
			break
		}
	}
	siteCache.entries[key] = siteCacheEntry{value, time.Now().Add(ttl)}
	siteCache.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, value)
	return nil
}
