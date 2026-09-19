package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// ntfyURL is the in-cluster address, deliberately not the public one.
// This job runs exactly when public DNS is wrong, so a notification that
// depends on public DNS is a notification that cannot arrive.
const ntfyURL = "http://ntfy.ntfy.svc.cluster.local/homelab-warning"

// notify tells the phone that the public path just moved.
//
// Best-effort: the DNS change already succeeded by the time this runs,
// and failing the job over an unsent notification would turn a working
// update into a red job and a retry that has nothing left to do.
//
// It is best-effort with a LOG, though, which the shell version it
// replaces was not. That one ended in `|| true` and had been posting
// with an empty bearer token - ntfy answers 403, the shell swallowed it,
// and every WAN change since had gone unannounced while the job still
// reported success.
func notify(ctx context.Context, hosts []string, wan string) {
	token := os.Getenv("NTFY_TOKEN")
	if token == "" {
		slog.Warn("NTFY_TOKEN is not set; the WAN change will not be announced",
			"hosts", hosts, "wan", wan)
		return
	}
	if err := post(ctx, token, hosts, wan); err != nil {
		slog.Warn("could not announce the WAN change", "err", err, "hosts", hosts, "wan", wan)
	}
}

func post(ctx context.Context, token string, hosts []string, wan string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body := fmt.Sprintf("%s now points at %s", strings.Join(hosts, ", "), wan)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ntfyURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Title", "WAN IP changed")
	req.Header.Set("Tags", "globe_with_meridians")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy answered %d", resp.StatusCode)
	}
	return nil
}
