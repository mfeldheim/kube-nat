package peer

import (
	"encoding/binary"
	"fmt"
)

const (
	MsgPing     byte = 0x01
	MsgPong     byte = 0x02
	MsgStepDown byte = 0x03

	msgSize = 9 // 1 byte type + 8 bytes unix nano
)

type Message struct {
	Type      byte
	Timestamp int64
}

func Encode(m Message) []byte {
	buf := make([]byte, msgSize)
	buf[0] = m.Type
	binary.BigEndian.PutUint64(buf[1:], uint64(m.Timestamp))
	return buf
}

func Decode(buf []byte) (Message, error) {
	if len(buf) < msgSize {
		return Message{}, fmt.Errorf("short message: %d bytes", len(buf))
	}
	return Message{
		Type:      buf[0],
		Timestamp: int64(binary.BigEndian.Uint64(buf[1:])),
	}, nil
}
