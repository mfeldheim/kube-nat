package spot

import (
	"context"
	"io"
	"net/http"
	"time"
)

type Watcher struct {
	baseURL  string
	interval time.Duration
	onNotice func()
	client   *http.Client
}

func NewWatcher(baseURL string, interval time.Duration, onNotice func()) *Watcher {
	if baseURL == "" {
		baseURL = "http://169.254.169.254"
	}
	return &Watcher{
		baseURL:  baseURL,
		interval: interval,
		onNotice: onNotice,
		client:   &http.Client{Timeout: 2 * time.Second},
	}
}

// Run polls the IMDS spot termination endpoint until ctx is done or a notice is received.
// Calls onNotice exactly once when a termination time is found.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.check(ctx) {
				w.onNotice()
				return
			}
		}
	}
}

func (w *Watcher) check(ctx context.Context) bool {
	token, err := w.getToken(ctx)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		w.baseURL+"/latest/meta-data/spot/termination-time", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

func (w *Watcher) getToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		w.baseURL+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
