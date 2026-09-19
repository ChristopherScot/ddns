# ddns

Owned by me-myself-and-i.

Points every publicly reachable hostname at the house's current WAN
address, every five minutes.

## Why it exists

Comcast rotates the WAN IP with no notice. On 2026-09-14 the record still
read `76.153.223.132` while the house had moved to `76.110.226.187`, so
every from-outside path died with a TCP timeout and nothing surfaced it.

## Why it replaced a shell script

The previous version managed ONE record, named by a `RECORD_NAME` env
var. By the time this was written the cluster served three public
hostnames: one was correct only because someone had fixed it by hand
after the last rotation, and a third had been pointing at the
pre-rotation address for days.

A list you have to remember to edit is the same bug one level up. So
this asks the cluster instead.

## How a hostname gets a DNS record

Mark it public in the service's `config.yaml`:

```yaml
ingress:
    hosts:
        - name: pokemon.chrisscotmartin.com
          tls: true
          public: true
```

homelabctl renders that as `ingressClassName: public`, this job finds it
on the next run, and Route 53 gets an A record pointing at the current
WAN address. `UPSERT` creates the record when it does not exist yet, so
there is nothing to create by hand first.

That is the whole contract. There is no list in this repo to keep in
sync — the discovery rule is the same bit of configuration that already
decides whether a hostname is reachable from outside, which is what
makes "served publicly but not resolved publicly" impossible rather than
merely unlikely.

## What it will not do

- **Wildcards.** `*.chrisscotmartin.com` is not an A record this job can
  own; it logs and skips.
- **Publish an address two echo services disagree on.** Both
  `api.ipify.org` and `ifconfig.me` must return the same public IPv4, or
  the run leaves DNS alone and exits 0. The next run is five minutes
  away; publishing a wrong address is not recoverable on that timescale.
- **Touch anything when nothing is stale.** It reads Route 53 directly
  rather than resolving the name, so the comparison is against what is
  actually published rather than a cached answer.

## Layout

```
main.go        the run: discover, compare, update, notify
wan.go         what the house's public address is
ingress.go     which hostnames are public, from the Kubernetes API
route53.go     reading and writing the hosted zone
notify.go      telling the phone the public path moved
rbac.yaml      get/list on ingresses, and nothing else
config.yaml    the source of truth; `homelabctl render` regenerates deploy/
```

## Credentials

Two, both reused rather than reissued:

| variable | from | why that one |
|---|---|---|
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `HOSTED_ZONE_ID` | `cert-manager/route53` | cert-manager's key already allows exactly `ChangeResourceRecordSets` and `ListResourceRecordSets` on this zone |
| `NTFY_TOKEN` | `ntfy/config` (`grafana_token`) | the shared token with publish access to `homelab-warning` |

Minting second copies under a `ddns/` path would mean two more secrets to
rotate and two more places for them to drift.

## Tests

`go test ./...` is hermetic. Three tests read real infrastructure and
skip unless pointed at it:

```sh
# discovery, against a real ingress list
kubectl get ingresses -A -o json > /tmp/ing.json
LIVE_INGRESSES=/tmp/ing.json go test -run RealCluster -v .

# reading the real zone; read-only, never upserts
AWS_PROFILE=personal LIVE_ZONE=Z0475915FBCYEAWH0Y68 go test -run RealRoute53 -v .

# the write path, on a throwaway name nothing serves
AWS_PROFILE=personal LIVE_ZONE_WRITE=Z0475915FBCYEAWH0Y68 go test -run UpsertAgainstReal -v .
```
