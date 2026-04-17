package iface_test

import (
	"testing"

	"github.com/kube-nat/kube-nat/internal/iface"
)

func TestInterfaceStatsStruct(t *testing.T) {
	s := iface.Stats{
		BytesTX:   1000,
		BytesRX:   2000,
		PacketsTX: 10,
		PacketsRX: 20,
	}
	if s.BytesTX != 1000 {
		t.Errorf("want 1000 got %d", s.BytesTX)
	}
	if s.BytesRX != 2000 {
		t.Errorf("want 2000 got %d", s.BytesRX)
	}
}

func TestConntrackCountMissingFile(t *testing.T) {
	_, err := iface.ReadConntrackCount("/nonexistent/path")
	if err == nil {
		t.Error("want error for missing file")
	}
}

func TestConntrackMaxMissingFile(t *testing.T) {
	_, err := iface.ReadConntrackMax("/nonexistent/path")
	if err == nil {
		t.Error("want error for missing file")
	}
}
