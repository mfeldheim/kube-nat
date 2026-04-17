package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type InstanceMetadata struct {
	InstanceID  string
	AZ          string
	Region      string
	ENIID       string
	MAC         string
	PublicIface string // Linux interface name, e.g. "eth0" (derived from device-number)
}

type MetadataClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewMetadataClient(baseURL string) *MetadataClient {
	if baseURL == "" {
		baseURL = "http://169.254.169.254"
	}
	return &MetadataClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *MetadataClient) Fetch(ctx context.Context) (*InstanceMetadata, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("imds token: %w", err)
	}
	get := func(path string) (string, error) {
		return c.get(ctx, token, path)
	}

	instanceID, err := get("/latest/meta-data/instance-id")
	if err != nil {
		return nil, err
	}
	az, err := get("/latest/meta-data/placement/availability-zone")
	if err != nil {
		return nil, err
	}
	region, err := get("/latest/meta-data/placement/region")
	if err != nil {
		return nil, err
	}
	mac, err := get("/latest/meta-data/mac")
	if err != nil {
		return nil, err
	}
	eniID, err := get("/latest/meta-data/network/interfaces/macs/" + mac + "/interface-id")
	if err != nil {
		return nil, err
	}
	deviceNum, err := get("/latest/meta-data/network/interfaces/macs/" + mac + "/device-number")
	if err != nil {
		return nil, err
	}
	iface, err := deviceNumberToIface(strings.TrimSpace(deviceNum))
	if err != nil {
		return nil, fmt.Errorf("device-number %q: %w", deviceNum, err)
	}

	return &InstanceMetadata{
		InstanceID:  instanceID,
		AZ:          az,
		Region:      region,
		ENIID:       eniID,
		MAC:         mac,
		PublicIface: iface,
	}, nil
}

// SpotTerminationTime returns the termination time and whether a notice is pending.
// Returns (zero, false, nil) when no notice is present (404 from IMDS).
func (c *MetadataClient) SpotTerminationTime(ctx context.Context) (time.Time, bool, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/latest/meta-data/spot/termination-time", nil)
	if err != nil {
		return time.Time{}, false, err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return time.Time{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return time.Time{}, false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, string(body))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse termination time %q: %w", string(body), err)
	}
	return t, true, nil
}

// deviceNumberToIface maps IMDS device-number (0-indexed) to Linux interface name.
// Device 0 → eth0, device 1 → eth1, etc. (Nitro convention).
func deviceNumberToIface(deviceNum string) (string, error) {
	n, err := strconv.Atoi(deviceNum)
	if err != nil {
		return "", fmt.Errorf("parse device number: %w", err)
	}
	return fmt.Sprintf("eth%d", n), nil
}

func (c *MetadataClient) getToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.baseURL+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("imds token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *MetadataClient) get(ctx context.Context, token, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
