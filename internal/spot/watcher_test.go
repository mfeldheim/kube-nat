package spot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/spot"
)

func TestNoInterruption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/api/token" {
			w.Write([]byte("token"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	notified := false
	w := spot.NewWatcher(srv.URL, 50*time.Millisecond, func() {
		notified = true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if notified {
		t.Error("should not notify when no interruption")
	}
}

func TestInterruptionNotifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/api/token" {
			w.Write([]byte("token"))
			return
		}
		if r.URL.Path == "/latest/meta-data/spot/termination-time" {
			w.Write([]byte("2026-04-17T15:30:00Z"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	notified := make(chan struct{}, 1)
	w := spot.NewWatcher(srv.URL, 20*time.Millisecond, func() {
		notified <- struct{}{}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go w.Run(ctx)

	select {
	case <-notified:
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for interruption notification")
	}
}
