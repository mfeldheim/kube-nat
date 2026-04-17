package peer

import (
	"context"
	"io"
	"net"
	"time"
)

type ClientConfig struct {
	ProbeInterval time.Duration
	ProbeFailures int
	OnFailure     func(az string)
	OnStepDown    func(az string)
}

type Client struct {
	az   string
	cfg  ClientConfig
	conn net.Conn
}

func NewClient(az string, cfg ClientConfig) *Client {
	return &Client{az: az, cfg: cfg}
}

// Connect dials addr and runs the heartbeat loop.
// Calls cfg.OnFailure(az) after ProbeFailures consecutive missed pongs.
// Runs until ctx is cancelled or failure is declared.
func (c *Client) Connect(ctx context.Context, addr string) {
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, c.cfg.ProbeInterval)
		if err != nil {
			failures++
			if failures >= c.cfg.ProbeFailures {
				if c.cfg.OnFailure != nil {
					c.cfg.OnFailure(c.az)
				}
				return
			}
			time.Sleep(c.cfg.ProbeInterval)
			continue
		}
		c.conn = conn
		failures = 0
		result := c.runHeartbeat(ctx, conn)
		conn.Close()
		c.conn = nil
		if result == heartbeatStepDown {
			if c.cfg.OnStepDown != nil {
				c.cfg.OnStepDown(c.az)
			}
			return
		}
		failures++
		if failures >= c.cfg.ProbeFailures {
			if c.cfg.OnFailure != nil {
				c.cfg.OnFailure(c.az)
			}
			return
		}
	}
}

// SendStepDown sends a step-down message over the current connection.
// Called on SIGTERM before the agent exits.
func (c *Client) SendStepDown() {
	if c.conn == nil {
		return
	}
	msg := Encode(Message{Type: MsgStepDown, Timestamp: time.Now().UnixNano()})
	c.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	c.conn.Write(msg)
}

type heartbeatResult int

const (
	heartbeatLost     heartbeatResult = iota
	heartbeatStepDown heartbeatResult = iota
)

func (c *Client) runHeartbeat(ctx context.Context, conn net.Conn) heartbeatResult {
	failures := 0
	buf := make([]byte, msgSize)
	ticker := time.NewTicker(c.cfg.ProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return heartbeatLost
		case <-ticker.C:
			ping := Encode(Message{Type: MsgPing, Timestamp: time.Now().UnixNano()})
			conn.SetWriteDeadline(time.Now().Add(c.cfg.ProbeInterval))
			if _, err := conn.Write(ping); err != nil {
				failures++
				if failures >= c.cfg.ProbeFailures {
					return heartbeatLost
				}
				continue
			}
			conn.SetReadDeadline(time.Now().Add(c.cfg.ProbeInterval))
			if _, err := io.ReadFull(conn, buf); err != nil {
				failures++
				if failures >= c.cfg.ProbeFailures {
					return heartbeatLost
				}
				continue
			}
			msg, err := Decode(buf)
			if err != nil {
				failures++
				continue
			}
			switch msg.Type {
			case MsgPong:
				failures = 0
			case MsgStepDown:
				return heartbeatStepDown
			}
		}
	}
}
