package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// PublicIngressClass is the class that marks a hostname as WAN-facing.
//
// This is the whole discovery rule, and it is deliberately the same bit
// of configuration that already decides whether a hostname is reachable
// from outside: a service becomes public by asking for this class in its
// config.yaml, and DNS follows. There is no second list to keep in sync,
// which is the entire point - a hostname that is served publicly but not
// resolved publicly is exactly the failure this job exists to prevent.
const PublicIngressClass = "public"

const (
	tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// ingressList is the slice of the Ingress API this job reads. Declared
// rather than using the typed client because three fields do not justify
// pulling k8s.io/api and its transitive half of the ecosystem into an
// image that otherwise has none of it.
type ingressList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			IngressClassName string `json:"ingressClassName"`
			Rules            []struct {
				Host string `json:"host"`
			} `json:"rules"`
		} `json:"spec"`
	} `json:"items"`
}

// publicHosts lists every hostname served by a public Ingress, across all
// namespaces, deduplicated and sorted.
func publicHosts(ctx context.Context) ([]string, error) {
	c, err := newKubeClient()
	if err != nil {
		return nil, err
	}
	var list ingressList
	if err := c.get(ctx, "/apis/networking.k8s.io/v1/ingresses", &list); err != nil {
		return nil, err
	}
	return hostsIn(list), nil
}

// hostsIn applies the discovery rule to an already-fetched list.
func hostsIn(list ingressList) []string {
	seen := map[string]bool{}
	for _, in := range list.Items {
		if in.Spec.IngressClassName != PublicIngressClass {
			continue
		}
		for _, r := range in.Spec.Rules {
			// A rule with no host matches everything, and there is no
			// name to publish for it.
			if r.Host == "" {
				continue
			}
			// Wildcards are a record this job cannot safely own: it
			// would have to decide what "*.example.com" resolves to,
			// and the answer is not one A record.
			if strings.HasPrefix(r.Host, "*") {
				slog.Warn("skipping a wildcard host: this job publishes A records for exact names",
					"host", r.Host,
					"ingress", in.Metadata.Namespace+"/"+in.Metadata.Name)
				continue
			}
			seen[r.Host] = true
		}
	}
	return sorted(seen)
}

type kubeClient struct {
	http  *http.Client
	token string
}

func newKubeClient() (*kubeClient, error) {
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading the service account token: %w", err)
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("the cluster CA at %s is not a PEM certificate", caFile)
	}
	return &kubeClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			},
		},
		token: strings.TrimSpace(string(token)),
	}, nil
}

func (c *kubeClient) get(ctx context.Context, path string, into any) error {
	url := "https://kubernetes.default.svc" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
