package main

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestSiteMetadataLive(t *testing.T) {
	if os.Getenv("DUOYU_METADATA_LIVE") != "1" {
		t.Skip("explicit live network test")
	}
	for _, raw := range []string{"https://xinquji.com/", "https://www.youtube.com/", "https://www.netflix.com/"} {
		t.Run(raw, func(t *testing.T) {
			u, _ := url.Parse(raw)
			ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
			defer cancel()
			_, _, _, probeError := readSite(ctx, "https://icons.duckduckgo.com/ip3/"+u.Hostname()+".ico")
			t.Logf("public cache connection: %v", probeError)
			result := resolveSite(ctx, u)
			t.Logf("title=%t icon=%t source=%s", result.Title != "", result.Icon != "", result.Source)
			if result.Icon == "" {
				t.Error("no icon from available sources")
			}
		})
	}
}
