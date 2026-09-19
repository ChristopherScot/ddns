package main

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// captureRoute53 points the SDK at a local server and returns the request
// body the client would have sent to AWS.
//
// The write path cannot be exercised against the real zone from here, so
// this checks the thing that would be wrong if it were: the batch AWS
// receives.
func captureRoute53(t *testing.T, body *string) *route53Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*body = string(b)
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, `<?xml version="1.0"?>
<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status>
  <SubmittedAt>2026-01-01T00:00:00Z</SubmittedAt></ChangeInfo>
</ChangeResourceRecordSetsResponse>`)
	}))
	t.Cleanup(srv.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &route53Client{api: route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = &srv.URL
	})}
}

// One batch for every stale name, each an UPSERT with the same address
// and an explicit TTL. Route 53 applies a batch atomically, so the names
// cannot end up half-updated.
func TestUpsertSendsOneAtomicBatch(t *testing.T) {
	var sent string
	c := captureRoute53(t, &sent)

	hosts := []string{"a.chrisscotmartin.com", "b.chrisscotmartin.com"}
	if err := c.upsert(context.Background(), "Z1", hosts, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}

	var batch struct {
		Changes []struct {
			Action string `xml:"Action"`
			RRSet  struct {
				Name    string `xml:"Name"`
				Type    string `xml:"Type"`
				TTL     int    `xml:"TTL"`
				Records []struct {
					Value string `xml:"Value"`
				} `xml:"ResourceRecords>ResourceRecord"`
			} `xml:"ResourceRecordSet"`
		} `xml:"ChangeBatch>Changes>Change"`
	}
	if err := xml.Unmarshal([]byte(sent), &batch); err != nil {
		t.Fatalf("parsing the request AWS would receive: %v\n%s", err, sent)
	}
	if len(batch.Changes) != len(hosts) {
		t.Fatalf("sent %d changes, want %d:\n%s", len(batch.Changes), len(hosts), sent)
	}
	for i, ch := range batch.Changes {
		if ch.Action != "UPSERT" {
			t.Errorf("change %d action = %q, want UPSERT", i, ch.Action)
		}
		if ch.RRSet.Name != hosts[i] {
			t.Errorf("change %d name = %q, want %q", i, ch.RRSet.Name, hosts[i])
		}
		if ch.RRSet.Type != "A" {
			t.Errorf("change %d type = %q, want A", i, ch.RRSet.Type)
		}
		if ch.RRSet.TTL != recordTTL {
			t.Errorf("change %d TTL = %d, want %d", i, ch.RRSet.TTL, recordTTL)
		}
		if len(ch.RRSet.Records) != 1 || ch.RRSet.Records[0].Value != "203.0.113.7" {
			t.Errorf("change %d records = %+v, want one 203.0.113.7", i, ch.RRSet.Records)
		}
	}
}

// Every change in a batch must carry its OWN name. Closing over the loop
// variable by address would point every record at the last host - which
// Route 53 accepts, because it is a valid batch.
func TestUpsertGivesEachChangeItsOwnName(t *testing.T) {
	var sent string
	c := captureRoute53(t, &sent)

	hosts := []string{"first.chrisscotmartin.com", "second.chrisscotmartin.com"}
	if err := c.upsert(context.Background(), "Z1", hosts, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if !strings.Contains(sent, "<Name>"+h+"</Name>") {
			t.Errorf("the batch never names %s:\n%s", h, sent)
		}
	}
}

// Route 53 answers with a trailing dot on every name; the hostnames an
// Ingress declares have none. Without stripping it, every host compares
// unequal and the job rewrites all of them on every run.
func TestARecordsStripsTheTrailingDot(t *testing.T) {
	if got := unqualify("ntfy.chrisscotmartin.com."); got != "ntfy.chrisscotmartin.com" {
		t.Errorf("unqualify() = %q, want no trailing dot", got)
	}
	if got := unqualify("ntfy.chrisscotmartin.com"); got != "ntfy.chrisscotmartin.com" {
		t.Errorf("unqualify() changed a name that had no dot: %q", got)
	}
}
