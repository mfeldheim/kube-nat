package peer

import (
	"context"
	"io"
	"net"
	"time"
)

type Server struct {
	addr     string
	listener net.Listener
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) Listen() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	return nil
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) Close() error {
	return s.listener.Close()
}

func (s *Server) Serve(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, msgSize)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		msg, err := Decode(buf)
		if err != nil {
			return
		}
		switch msg.Type {
		case MsgPing:
			pong := Encode(Message{Type: MsgPong, Timestamp: time.Now().UnixNano()})
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			conn.Write(pong)
		case MsgStepDown:
			return
		}
	}
}
