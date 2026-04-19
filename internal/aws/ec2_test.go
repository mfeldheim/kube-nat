package aws_test

import (
	"context"
	"testing"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
)

type fakeEC2 struct {
	srcDstDisabled bool
	routeTables    []kubenataws.RouteTable
	routes         map[string]string
}

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error {
	f.srcDstDisabled = true
	return nil
}

func (f *fakeEC2) DiscoverRouteTables(_ context.Context, az string) ([]kubenataws.RouteTable, error) {
	var result []kubenataws.RouteTable
	for _, rt := range f.routeTables {
		if rt.AZ == az {
			result = append(result, rt)
		}
	}
	return result, nil
}

func (f *fakeEC2) ClaimRouteTable(_ context.Context, rtbID, instanceID string) error {
	if f.routes == nil {
		f.routes = make(map[string]string)
	}
	f.routes[rtbID] = instanceID
	return nil
}

func (f *fakeEC2) ReleaseRouteTable(_ context.Context, rtbID, _ string) error {
	delete(f.routes, rtbID)
	return nil
}

func (f *fakeEC2) LookupNatGateway(_ context.Context, _, _ string) (string, error) {
	return "nat-lookup", nil
}

func (f *fakeEC2) DescribeInstanceMaxBandwidth(_ context.Context, _ string) (float64, error) {
	return 25e9, nil // 25 Gbps
}

func TestEC2Interface(t *testing.T) {
	var _ kubenataws.EC2Client = &fakeEC2{}
}

func TestDiscoverRouteTables(t *testing.T) {
	f := &fakeEC2{
		routeTables: []kubenataws.RouteTable{
			{ID: "rtb-001", AZ: "eu-west-1a"},
			{ID: "rtb-002", AZ: "eu-west-1b"},
		},
	}
	tables, err := f.DiscoverRouteTables(context.Background(), "eu-west-1a")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("want 1 table got %d", len(tables))
	}
	if tables[0].ID != "rtb-001" {
		t.Errorf("want rtb-001 got %s", tables[0].ID)
	}
}

func TestClaimRouteTable(t *testing.T) {
	f := &fakeEC2{}
	err := f.ClaimRouteTable(context.Background(), "rtb-001", "i-0abc")
	if err != nil {
		t.Fatal(err)
	}
	if f.routes["rtb-001"] != "i-0abc" {
		t.Errorf("want i-0abc got %s", f.routes["rtb-001"])
	}
}
