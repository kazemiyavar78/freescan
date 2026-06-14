package freescan

import "encoding/binary"

const (
	MsgSize    = 12
	FixedWord1 = 0x00000004

	CmdPoll  uint32 = 0x00000001
	CmdOpen  uint32 = 0x00000002
	CmdClose uint32 = 0x00000003
	CmdAck   uint32 = 0x00000004 // TODO: may be CMD_DONE — confirm with more captures

	StatusReady    uint32 = 0x00000010
	StatusScanning uint32 = 0x00000011
	StatusBusy     uint32 = 0x00000012

	PollParam uint32 = 0x000003FC
)

// Message is a 12-byte little-endian protocol frame.
type Message struct {
	Code   uint32
	Marker uint32
	Param  uint32
}

// Encode serializes the message to a 12-byte buffer.
func (m Message) Encode() []byte {
	buf := make([]byte, MsgSize)
	binary.LittleEndian.PutUint32(buf[0:4], m.Code)
	binary.LittleEndian.PutUint32(buf[4:8], m.Marker)
	binary.LittleEndian.PutUint32(buf[8:12], m.Param)
	return buf
}

// Decode parses a 12-byte buffer into a Message.
func Decode(buf []byte) Message {
	return Message{
		Code:   binary.LittleEndian.Uint32(buf[0:4]),
		Marker: binary.LittleEndian.Uint32(buf[4:8]),
		Param:  binary.LittleEndian.Uint32(buf[8:12]),
	}
}

// NewCommand builds an encoded host command frame.
func NewCommand(code, param uint32) []byte {
	return Message{Code: code, Marker: FixedWord1, Param: param}.Encode()
}

// StatusName returns a human-readable name for a status code.
func StatusName(code uint32) string {
	switch code {
	case StatusReady:
		return "STATUS_READY"
	case StatusScanning:
		return "STATUS_SCANNING"
	case StatusBusy:
		return "STATUS_BUSY"
	default:
		return "UNKNOWN"
	}
}
