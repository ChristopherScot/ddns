package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// decoded parses an Ingress list the way the API server sends one, so
// the struct tags are under test too and not just the filtering.
func decoded(t *testing.T, body string) ingressList {
	t.Helper()
	var list ingressList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	return list
}

const clusterFixture = `{"items":[
 {"metadata":{"name":"ntfy","namespace":"ntfy"},
  "spec":{"ingressClassName":"public","rules":[{"host":"ntfy.chrisscotmartin.com"}]}},
 {"metadata":{"name":"grafana","namespace":"monitoring"},
  "spec":{"ingressClassName":"external","rules":[{"host":"grafana.home.chrisscotmartin.com"}]}},
 {"metadata":{"name":"pokedex-web-public","namespace":"pokedex-web"},
  "spec":{"ingressClassName":"public","rules":[{"host":"pokemon.chrisscotmartin.com"}]}},
 {"metadata":{"name":"pokedex-web","namespace":"pokedex-web"},
  "spec":{"ingressClassName":"external","rules":[{"host":"pokemon.home.chrisscotmartin.com"}]}}
]}`

// The bug this whole rewrite exists to fix: the shell version managed one
// record name from an env var, so a second public service was hand-set
// and a third silently went stale. Discovery must return EVERY public
// hostname, and only the public ones.
func TestPublicHostsFindsEveryPublicNameAndNothingElse(t *testing.T) {
	got := hostsIn(decoded(t, clusterFixture))
	want := []string{"ntfy.chrisscotmartin.com", "pokemon.chrisscotmartin.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hostsIn() = %v, want %v", got, want)
	}
}

// An internal-only cluster must produce no changes at all, rather than
// an empty-ish batch that touches DNS.
func TestPublicHostsIsEmptyWhenNothingIsPublic(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"a","namespace":"a"},
	  "spec":{"ingressClassName":"external","rules":[{"host":"a.home.chrisscotmartin.com"}]}}]}`
	if got := hostsIn(decoded(t, body)); len(got) != 0 {
		t.Errorf("hostsIn() = %v, want nothing", got)
	}
}

// An Ingress with no class set is not public. Defaulting the other way
// would publish every internal hostname to the open internet the first
// time someone omitted the field.
func TestPublicHostsTreatsAMissingClassAsPrivate(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"a","namespace":"a"},
	  "spec":{"rules":[{"host":"a.home.chrisscotmartin.com"}]}}]}`
	if got := hostsIn(decoded(t, body)); len(got) != 0 {
		t.Errorf("hostsIn() = %v, want nothing", got)
	}
}

// Two public Ingresses may serve the same name; the change batch must
// not then contain it twice, which Route 53 rejects outright.
//
// A set makes that impossible rather than merely unlikely, so this test
// does not catch a mutation - it pins the guarantee so that swapping the
// set for a slice has to fail here first.
func TestPublicHostsDeduplicates(t *testing.T) {
	body := `{"items":[
	 {"metadata":{"name":"a","namespace":"a"},
	  "spec":{"ingressClassName":"public","rules":[{"host":"x.chrisscotmartin.com"}]}},
	 {"metadata":{"name":"b","namespace":"b"},
	  "spec":{"ingressClassName":"public","rules":[{"host":"x.chrisscotmartin.com"}]}}]}`
	got := hostsIn(decoded(t, body))
	if want := []string{"x.chrisscotmartin.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hostsIn() = %v, want %v", got, want)
	}
}

// A rule with no host matches every request and names nothing, and a
// wildcard is not an A record this job can own.
func TestPublicHostsSkipsHostsItCannotPublish(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"a","namespace":"a"},
	  "spec":{"ingressClassName":"public","rules":[
	    {"host":""},{"host":"*.chrisscotmartin.com"},{"host":"real.chrisscotmartin.com"}]}}]}`
	got := hostsIn(decoded(t, body))
	if want := []string{"real.chrisscotmartin.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hostsIn() = %v, want %v", got, want)
	}
}

// One Ingress can carry several public names.
func TestPublicHostsReadsEveryRule(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"a","namespace":"a"},
	  "spec":{"ingressClassName":"public","rules":[
	    {"host":"b.chrisscotmartin.com"},{"host":"a.chrisscotmartin.com"}]}}]}`
	got := hostsIn(decoded(t, body))
	want := []string{"a.chrisscotmartin.com", "b.chrisscotmartin.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hostsIn() = %v, want %v", got, want)
	}
}
