package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const (
	MsgRowBatch  = 0x01
	MsgCompleted = 0x02
	MsgError     = 0x03
	MsgHeartbeat = 0x04
)

type Message struct {
	Type    byte
	Payload []byte
}

func EncodeRowBatch(rows []map[string]interface{}) ([]byte, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("marshal rows: %w", err)
	}

	msg := make([]byte, 1+4+len(data))
	msg[0] = MsgRowBatch
	binary.BigEndian.PutUint32(msg[1:5], uint32(len(data)))
	copy(msg[5:], data)

	return msg, nil
}

func EncodeCompleted() []byte {
	msg := make([]byte, 5)
	msg[0] = MsgCompleted
	binary.BigEndian.PutUint32(msg[1:5], 0)
	return msg
}

func EncodeError(errMsg string) []byte {
	data := []byte(errMsg)
	msg := make([]byte, 1+4+len(data))
	msg[0] = MsgError
	binary.BigEndian.PutUint32(msg[1:5], uint32(len(data)))
	copy(msg[5:], data)
	return msg
}

func EncodeHeartbeat() []byte {
	msg := make([]byte, 5)
	msg[0] = MsgHeartbeat
	binary.BigEndian.PutUint32(msg[1:5], 0)
	return msg
}

type Sender interface {
	SendToQuery(queryID string, data []byte) error
}
