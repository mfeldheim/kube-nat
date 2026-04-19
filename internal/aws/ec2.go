package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type RouteTable struct {
	ID           string
	AZ           string
	VpcID        string // VPC this route table belongs to
	NatGatewayID string // set if 0.0.0.0/0 currently points to a NAT gateway
	InstanceID   string // set if 0.0.0.0/0 currently points to an instance
}

// EC2Client is the interface for all AWS EC2 operations kube-nat needs.
type EC2Client interface {
	DisableSourceDestCheck(ctx context.Context, eniID string) error
	DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error)
	ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error
	ReleaseRouteTable(ctx context.Context, rtbID, natGatewayID string) error
	LookupNatGateway(ctx context.Context, vpcID, az string) (string, error)
}

type realEC2Client struct {
	svc            *ec2.Client
	tagPrefix      string
	discoveryValue string // when non-empty, filter by tagPrefix/discovery=value; else tagPrefix/managed=true
}

func NewEC2Client(ctx context.Context, region, tagPrefix, discoveryValue string) (EC2Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &realEC2Client{
		svc:            ec2.NewFromConfig(cfg),
		tagPrefix:      tagPrefix,
		discoveryValue: discoveryValue,
	}, nil
}

func (c *realEC2Client) DisableSourceDestCheck(ctx context.Context, eniID string) error {
	_, err := c.svc.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(eniID),
		SourceDestCheck:    &types.AttributeBooleanValue{Value: aws.Bool(false)},
	})
	return err
}

func (c *realEC2Client) DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error) {
	managedFilter := types.Filter{
		Name:   aws.String("tag:" + c.tagPrefix + "/managed"),
		Values: []string{"true"},
	}
	if c.discoveryValue != "" {
		managedFilter = types.Filter{
			Name:   aws.String("tag:" + c.tagPrefix + "/discovery"),
			Values: []string{c.discoveryValue},
		}
	}
	out, err := c.svc.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			managedFilter,
			{
				Name:   aws.String("tag:" + c.tagPrefix + "/az"),
				Values: []string{az},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("describe route tables: %w", err)
	}
	tables := make([]RouteTable, 0, len(out.RouteTables))
	for _, rt := range out.RouteTables {
		entry := RouteTable{ID: *rt.RouteTableId, AZ: az}
		if rt.VpcId != nil {
			entry.VpcID = *rt.VpcId
		}
		for _, r := range rt.Routes {
			if r.DestinationCidrBlock != nil && *r.DestinationCidrBlock == "0.0.0.0/0" {
				if r.NatGatewayId != nil {
					entry.NatGatewayID = *r.NatGatewayId
				}
				if r.InstanceId != nil {
					entry.InstanceID = *r.InstanceId
				}
				break
			}
		}
		tables = append(tables, entry)
	}
	return tables, nil
}

func (c *realEC2Client) ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error {
	_, err := c.svc.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		InstanceId:           aws.String(instanceID),
	})
	if err != nil {
		return fmt.Errorf("replace route in %s: %w", rtbID, err)
	}
	return nil
}

func (c *realEC2Client) ReleaseRouteTable(ctx context.Context, rtbID, natGatewayID string) error {
	_, err := c.svc.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		NatGatewayId:         aws.String(natGatewayID),
	})
	if err != nil {
		return fmt.Errorf("restore nat gateway route in %s: %w", rtbID, err)
	}
	return nil
}

// LookupNatGateway finds an available NAT gateway in the given VPC, preferring
// the same AZ. Falls back to any available NAT gateway in the VPC if none exists
// in the preferred AZ (e.g. single shared NAT GW, or AZ NAT GW was deleted).
func (c *realEC2Client) LookupNatGateway(ctx context.Context, vpcID, preferAZ string) (string, error) {
	// Step 1: all available NAT gateways in this VPC.
	nats, err := c.svc.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
		Filter: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe nat gateways vpc=%s: %w", vpcID, err)
	}
	if len(nats.NatGateways) == 0 {
		return "", fmt.Errorf("no available nat gateways in vpc=%s", vpcID)
	}

	// Step 2: collect subnet IDs so we can resolve their AZs.
	subnetIDs := make([]string, 0, len(nats.NatGateways))
	natBySubnet := make(map[string]string, len(nats.NatGateways)) // subnetID → natGwID
	for _, n := range nats.NatGateways {
		if n.NatGatewayId != nil && n.SubnetId != nil {
			subnetIDs = append(subnetIDs, *n.SubnetId)
			natBySubnet[*n.SubnetId] = *n.NatGatewayId
		}
	}

	subnets, err := c.svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{Name: aws.String("subnet-id"), Values: subnetIDs},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnets for nat gateways in vpc=%s: %w", vpcID, err)
	}

	// Step 3: prefer same-AZ, fall back to any available.
	var anyGW string
	for _, s := range subnets.Subnets {
		if s.SubnetId == nil || s.AvailabilityZone == nil {
			continue
		}
		natGwID := natBySubnet[*s.SubnetId]
		if anyGW == "" {
			anyGW = natGwID
		}
		if *s.AvailabilityZone == preferAZ {
			return natGwID, nil
		}
	}
	if anyGW != "" {
		return anyGW, nil
	}
	return "", fmt.Errorf("no available nat gateway found in vpc=%s", vpcID)
}
