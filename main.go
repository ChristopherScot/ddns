// Dynamic DNS: points every publicly reachable hostname at the house's
// current WAN address.
//
// Why this exists: Comcast rotates the WAN IP with no notice. On
// 2026-09-14 the record still read 76.153.223.132 while the house had
// moved to 76.110.226.187, so every from-outside path died with a TCP
// timeout and nothing surfaced the breakage.
//
// Why it replaced a shell script: that version managed ONE record name,
// passed in as an env var. A second public service appeared and was
// hand-set beside it, correct only because someone fixed it after the
// last rotation; a third was still pointing at the pre-rotation address
// and had been broken for days. A list you have to remember to edit is
// the same bug one level up, so this asks the cluster instead.
//
// The discovery rule is `ingressClassName: public`. homelabctl renders
// that from `public: true` on a host, so adding a public hostname is a
// config.yaml edit and nothing else - no record to create by hand, no
// list to update here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

func main() {
	setupLogging()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("ddns failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	wan, err := wanIP(ctx)
	if err != nil {
		// Not an error exit: a flaky echo service is not a reason to
		// touch DNS, and the next run is five minutes away. Leaving the
		// records alone is always safe; publishing a wrong address is
		// not.
		slog.Warn("could not determine the WAN address; leaving DNS alone", "err", err)
		return nil
	}

	hosts, err := publicHosts(ctx)
	if err != nil {
		return fmt.Errorf("discovering public hostnames: %w", err)
	}
	if len(hosts) == 0 {
		slog.Info("no public hostnames in the cluster; nothing to do")
		return nil
	}

	zone := os.Getenv("HOSTED_ZONE_ID")
	if zone == "" {
		return fmt.Errorf("HOSTED_ZONE_ID is not set")
	}

	r53, err := newRoute53(ctx)
	if err != nil {
		return err
	}

	current, err := r53.aRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("reading %s: %w", zone, err)
	}

	var stale []string
	for _, h := range hosts {
		if current[h] == wan {
			continue
		}
		slog.Info("record is wrong", "host", h, "is", orUnset(current[h]), "want", wan)
		stale = append(stale, h)
	}
	if len(stale) == 0 {
		slog.Info("every public hostname is in sync", "wan", wan, "hosts", len(hosts))
		return nil
	}

	// One batch. Route 53 applies it atomically, so the names cannot
	// end up half-updated if the call fails partway.
	if err := r53.upsert(ctx, zone, stale, wan); err != nil {
		return fmt.Errorf("updating %v: %w", stale, err)
	}
	slog.Info("updated", "hosts", stale, "wan", wan)

	notify(ctx, stale, wan)
	return nil
}

func orUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

// setupLogging matches what every other service here emits, so one Loki
// query covers them all.
func setupLogging() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)).With(
		"service", "ddns",
		"team", envOr("TEAM", "me-myself-and-i"),
		"version", envOr("VERSION", "dev"),
	))
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
