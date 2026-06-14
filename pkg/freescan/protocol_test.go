package freescan_test

import (
	"bytes"
	"testing"

	"github.com/freescan/freescan/pkg/freescan"
)

func TestMessageEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		msg  freescan.Message
	}{
		{
			name: "CMD_POLL",
			msg: freescan.Message{
				Code:   freescan.CmdPoll,
				Marker: freescan.FixedWord1,
				Param:  freescan.PollParam,
			},
		},
		{
			name: "CMD_OPEN",
			msg: freescan.Message{
				Code:   freescan.CmdOpen,
				Marker: freescan.FixedWord1,
				Param:  0,
			},
		},
		{
			name: "STATUS_READY",
			msg: freescan.Message{
				Code:   freescan.StatusReady,
				Marker: freescan.FixedWord1,
				Param:  0xFF010001,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.msg.Encode()
			if len(encoded) != freescan.MsgSize {
				t.Fatalf("encoded length = %d, want %d", len(encoded), freescan.MsgSize)
			}
			decoded := freescan.Decode(encoded)
			if decoded != tc.msg {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", decoded, tc.msg)
			}
		})
	}
}

func TestNewCommandBytes(t *testing.T) {
	poll := freescan.NewCommand(freescan.CmdPoll, freescan.PollParam)
	want := []byte{0x01, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0xFC, 0x03, 0x00, 0x00}
	if !bytes.Equal(poll, want) {
		t.Fatalf("CMD_POLL bytes mismatch\ngot:  % x\nwant: % x", poll, want)
	}

	open := freescan.NewCommand(freescan.CmdOpen, 0)
	wantOpen := []byte{0x02, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(open, wantOpen) {
		t.Fatalf("CMD_OPEN bytes mismatch\ngot:  % x\nwant: % x", open, wantOpen)
	}

	closeCmd := freescan.NewCommand(freescan.CmdClose, 0)
	wantClose := []byte{0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(closeCmd, wantClose) {
		t.Fatalf("CMD_CLOSE bytes mismatch\ngot:  % x\nwant: % x", closeCmd, wantClose)
	}
}

func TestDecodeStatusReady(t *testing.T) {
	raw := []byte{0x10, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0xFF}
	msg := freescan.Decode(raw)
	if msg.Code != freescan.StatusReady {
		t.Fatalf("code = 0x%x, want STATUS_READY (0x10)", msg.Code)
	}
	if msg.Marker != freescan.FixedWord1 {
		t.Fatalf("marker = 0x%x, want 0x4", msg.Marker)
	}
	if msg.Param != 0xFF010001 {
		t.Fatalf("param = 0x%x, want 0xFF010001", msg.Param)
	}
}

func TestStatusName(t *testing.T) {
	if freescan.StatusName(freescan.StatusReady) != "STATUS_READY" {
		t.Fatal("expected STATUS_READY")
	}
	if freescan.StatusName(freescan.StatusBusy) != "STATUS_BUSY" {
		t.Fatal("expected STATUS_BUSY")
	}
	if freescan.StatusName(0x99) != "UNKNOWN" {
		t.Fatal("expected UNKNOWN")
	}
}
