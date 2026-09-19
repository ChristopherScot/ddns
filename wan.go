package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// echoServices are asked what the WAN address is.
//
// Two of them, independently operated, and both must agree. One being
// down or wrong should not publish a bogus address to DNS - the cost of
// waiting five minutes for the next run is nothing, and the cost of
// pointing every public hostname at a stranger's IP is real.
var echoServices = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
}

func wanIP(ctx context.Context) (string, error) {
	var got []string
	for _, url := range echoServices {
		ip, err := askEcho(ctx, url)
		if err != nil {
			return "", fmt.Errorf("%s: %w", url, err)
		}
		got = append(got, ip)
	}
	for _, ip := range got[1:] {
		if ip != got[0] {
			return "", fmt.Errorf("echo services disagree: %v", got)
		}
	}
	return got[0], nil
}

func askEcho(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	// Bounded: an echo service should return an address, and anything
	// longer is a captive portal or an error page.
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(b))

	// Parsed, not pattern-matched. A string that merely looks like four
	// numbers is not an address, and this is what gets published.
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("not an IPv4 address: %q", ip)
	}
	if !parsed.IsGlobalUnicast() || parsed.IsPrivate() {
		return "", fmt.Errorf("not a public address: %q", ip)
	}
	return parsed.String(), nil
}
