package iface

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

type Stats struct {
	BytesTX   uint64
	BytesRX   uint64
	PacketsTX uint64
	PacketsRX uint64
}

// GetStats returns TX/RX byte and packet counters for the named interface.
func GetStats(ifaceName string) (*Stats, error) {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("link %s: %w", ifaceName, err)
	}
	attrs := link.Attrs()
	if attrs.Statistics == nil {
		return nil, fmt.Errorf("no statistics for interface %s", ifaceName)
	}
	return &Stats{
		BytesTX:   attrs.Statistics.TxBytes,
		BytesRX:   attrs.Statistics.RxBytes,
		PacketsTX: attrs.Statistics.TxPackets,
		PacketsRX: attrs.Statistics.RxPackets,
	}, nil
}
