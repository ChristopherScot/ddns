package main

import (
	"context"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// recordTTL is short on purpose: it bounds how long a stale answer can
// outlive a WAN change. Five minutes of caching on top of a five-minute
// check is the worst case, and the records are cheap.
const recordTTL = 300

type route53Client struct {
	api *route53.Client
}

// newRoute53 builds a client from the standard AWS environment
// variables, which the ExternalSecret supplies as AWS_ACCESS_KEY_ID and
// AWS_SECRET_ACCESS_KEY.
//
// Route 53 is a global service whose endpoint lives in us-east-1. The
// credential carries no region of its own, so without this the SDK
// fails at call time with "an AWS region is required" rather than at
// construction.
func newRoute53(ctx context.Context) (*route53Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		return nil, fmt.Errorf("loading AWS credentials: %w", err)
	}
	return &route53Client{api: route53.NewFromConfig(cfg)}, nil
}

// aRecords returns the zone's A records as host -> address.
//
// It reads Route 53 rather than resolving the name, so the comparison is
// against what is actually published rather than against whatever a
// resolver still has cached.
func (c *route53Client) aRecords(ctx context.Context, zone string) (map[string]string, error) {
	out := map[string]string{}
	p := route53.NewListResourceRecordSetsPaginator(c.api, &route53.ListResourceRecordSetsInput{
		HostedZoneId: &zone,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, rr := range page.ResourceRecordSets {
			if rr.Type != types.RRTypeA || rr.Name == nil {
				continue
			}
			// An alias record has no ResourceRecords and is not a
			// value this job can own or compare against.
			if len(rr.ResourceRecords) == 0 || rr.ResourceRecords[0].Value == nil {
				continue
			}
			out[unqualify(*rr.Name)] = *rr.ResourceRecords[0].Value
		}
	}
	return out, nil
}

// upsert points every named host at addr, in one atomic batch.
//
// UPSERT creates the record when it does not exist yet, which is what
// makes "add a public hostname to config.yaml" enough on its own: the
// first run after a deploy provisions the record rather than requiring
// someone to have made it by hand first.
func (c *route53Client) upsert(ctx context.Context, zone string, hosts []string, addr string) error {
	changes := make([]types.Change, 0, len(hosts))
	for _, h := range hosts {
		host := h
		value := addr
		changes = append(changes, types.Change{
			Action: types.ChangeActionUpsert,
			ResourceRecordSet: &types.ResourceRecordSet{
				Name:            &host,
				Type:            types.RRTypeA,
				TTL:             ptr(int64(recordTTL)),
				ResourceRecords: []types.ResourceRecord{{Value: &value}},
			},
		})
	}
	comment := "ddns auto-update"
	_, err := c.api.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &zone,
		ChangeBatch: &types.ChangeBatch{
			Comment: &comment,
			Changes: changes,
		},
	})
	return err
}

// remove deletes a record. Only the self-test uses it: this job's whole
// job is to keep records pointing somewhere, and removing one when a
// hostname stops being public is a decision with no safe default - the
// name may be served by something else, or be about to come back.
func (c *route53Client) remove(ctx context.Context, zone, host, addr string) error {
	value := addr
	name := host
	_, err := c.api.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &zone,
		ChangeBatch: &types.ChangeBatch{Changes: []types.Change{{
			Action: types.ChangeActionDelete,
			ResourceRecordSet: &types.ResourceRecordSet{
				Name:            &name,
				Type:            types.RRTypeA,
				TTL:             ptr(int64(recordTTL)),
				ResourceRecords: []types.ResourceRecord{{Value: &value}},
			},
		}}},
	})
	return err
}

// unqualify strips the trailing dot Route 53 puts on every name, so the
// keys compare directly against the hostnames an Ingress declares.
func unqualify(name string) string {
	return strings.TrimSuffix(name, ".")
}

func ptr[T any](v T) *T { return &v }
