package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// Tests that read REAL infrastructure, each skipped unless pointed at
// it. `go test ./...` stays hermetic; these are opted into deliberately.
//
// They exist because the interesting failures in this job are not logic
// errors - they are struct tags that do not match what the API server
// sends, a credential without the permission it needs, a trailing dot
// nobody remembered. None of those show up against a fixture I wrote,
// because I would write the fixture to match my assumption.

// Drives the discovery filter against a real `kubectl get ingresses -A -o
// json` payload, so the struct tags are checked against what the API
// server actually sends rather than against a fixture I wrote.
//
// Skips unless LIVE_INGRESSES points at one.
func TestAgainstRealClusterPayload(t *testing.T) {
	path := os.Getenv("LIVE_INGRESSES")
	if path == "" {
		t.Skip("set LIVE_INGRESSES to a kubectl ingress dump")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list ingressList
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d ingresses in the payload", len(list.Items))
	got := hostsIn(list)
	t.Logf("public hostnames: %v", got)
	if len(got) == 0 {
		t.Error("found no public hostnames in a cluster that has some")
	}
}

// Reads the real hosted zone and reports what the job would decide.
// Read-only: it never calls upsert.
//
// Skips unless LIVE_ZONE is set, so `go test ./...` in CI stays hermetic.
func TestAgainstRealRoute53(t *testing.T) {
	zone := os.Getenv("LIVE_ZONE")
	if zone == "" {
		t.Skip("set LIVE_ZONE to read the real hosted zone")
	}
	ctx := context.Background()

	r53, err := newRoute53(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err := r53.aRecords(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d A records in the zone", len(current))

	wan, err := wanIP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wan = %s", wan)

	for _, h := range []string{
		"approve.chrisscotmartin.com",
		"ntfy.chrisscotmartin.com",
		"pokemon.chrisscotmartin.com",
	} {
		t.Logf("%-36s is %-16s %s", h, current[h],
			map[bool]string{true: "in sync", false: "WOULD UPDATE"}[current[h] == wan])
	}
}

// Exercises the write path end to end on a throwaway name, then deletes
// it.
//
// The name is one nothing serves and nothing resolves, so a failure
// halfway through leaves a stray record and breaks nothing. Cleanup is
// registered before the first write, so even a t.Fatal below leaves the
// zone as it was found.
//
// Skips unless LIVE_ZONE_WRITE is set: `go test ./...` stays hermetic,
// and writing to real DNS is something you opt into deliberately.
func TestUpsertAgainstRealRoute53(t *testing.T) {
	zone := os.Getenv("LIVE_ZONE_WRITE")
	if zone == "" {
		t.Skip("set LIVE_ZONE_WRITE to exercise the write path")
	}
	const host = "ddns-selftest.chrisscotmartin.com"
	ctx := context.Background()

	r53, err := newRoute53(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		// The last value written, which is what DELETE has to match.
		if err := r53.remove(context.Background(), zone, host, "192.0.2.2"); err != nil {
			t.Errorf("could not delete %s - remove it by hand: %v", host, err)
			return
		}
		left, err := r53.aRecords(context.Background(), zone)
		if err != nil {
			t.Errorf("could not confirm %s is gone: %v", host, err)
			return
		}
		if _, still := left[host]; still {
			t.Errorf("%s is still in the zone after cleanup", host)
		}
	})

	if err := r53.upsert(ctx, zone, []string{host}, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	after, err := r53.aRecords(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	if after[host] != "192.0.2.1" {
		t.Errorf("after create: %s = %q, want 192.0.2.1", host, after[host])
	}

	// UPSERT, not just create: the same call has to move an existing
	// record, which is the case that actually runs in production.
	if err := r53.upsert(ctx, zone, []string{host}, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	after, err = r53.aRecords(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	if after[host] != "192.0.2.2" {
		t.Errorf("after update: %s = %q, want 192.0.2.2", host, after[host])
	}
}
