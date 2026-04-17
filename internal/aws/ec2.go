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
	ID string
	AZ string
}

// EC2Client is the interface for all AWS EC2 operations kube-nat needs.
type EC2Client interface {
	DisableSourceDestCheck(ctx context.Context, eniID string) error
	DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error)
	ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error
}

type realEC2Client struct {
	svc       *ec2.Client
	tagPrefix string
}

func NewEC2Client(ctx context.Context, region, tagPrefix string) (EC2Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &realEC2Client{
		svc:       ec2.NewFromConfig(cfg),
		tagPrefix: tagPrefix,
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
	out, err := c.svc.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:" + c.tagPrefix + "/managed"),
				Values: []string{"true"},
			},
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
		tables = append(tables, RouteTable{ID: *rt.RouteTableId, AZ: az})
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
