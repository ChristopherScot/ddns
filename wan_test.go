package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// echoing swaps echoServices for stubs returning the given bodies, and
// restores the real list afterwards.
func echoing(t *testing.T, bodies ...string) {
	t.Helper()
	var urls []string
	for _, b := range bodies {
		body := b
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		t.Cleanup(s.Close)
		urls = append(urls, s.URL)
	}
	real := echoServices
	echoServices = urls
	t.Cleanup(func() { echoServices = real })
}

func TestWANIPNeedsAgreement(t *testing.T) {
	echoing(t, "203.0.113.7", "203.0.113.7")
	got, err := wanIP(context.Background())
	if err != nil {
		t.Fatalf("wanIP() = %v", err)
	}
	if got != "203.0.113.7" {
		t.Errorf("wanIP() = %q, want 203.0.113.7", got)
	}
}

// The failure this guards: one echo service lies or is served a captive
// portal, and the job publishes its answer to every public hostname.
func TestWANIPRefusesWhenEchoServicesDisagree(t *testing.T) {
	echoing(t, "203.0.113.7", "198.51.100.1")
	if got, err := wanIP(context.Background()); err == nil {
		t.Errorf("wanIP() = %q with no error, want a refusal", got)
	}
}

// Anything that is not a public IPv4 address must be refused rather than
// published: an error page, a private address, a bare hostname.
func TestWANIPRefusesNonPublicAnswers(t *testing.T) {
	for _, body := range []string{
		"",
		"not an address",
		"<html>error</html>",
		"192.168.50.1",
		"10.0.0.1",
		"127.0.0.1",
		"2001:db8::1",
	} {
		t.Run(body, func(t *testing.T) {
			echoing(t, body, body)
			if got, err := wanIP(context.Background()); err == nil {
				t.Errorf("wanIP() = %q with no error, want a refusal", got)
			}
		})
	}
}

// Whitespace is normal in these responses - ifconfig.me ends with a
// newline - and must not defeat the parse.
func TestWANIPToleratesSurroundingWhitespace(t *testing.T) {
	echoing(t, "203.0.113.7\n", "  203.0.113.7  ")
	got, err := wanIP(context.Background())
	if err != nil {
		t.Fatalf("wanIP() = %v", err)
	}
	if got != "203.0.113.7" {
		t.Errorf("wanIP() = %q, want 203.0.113.7", got)
	}
}
