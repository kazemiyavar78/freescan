package freescan_test

import (
	"bytes"
	"testing"

	"github.com/freescan/freescan/pkg/freescan"
)

func TestIsSyncPacket(t *testing.T) {
	sync := bytes.Repeat([]byte{0xa5}, 512)
	if !freescan.IsSyncPacket(sync) {
		t.Fatal("expected 512 × 0xa5 to be a sync packet")
	}

	short := bytes.Repeat([]byte{0xa5}, 8)
	if freescan.IsSyncPacket(short) {
		t.Fatal("expected short packet to not be a sync packet")
	}

	mixed := append(bytes.Repeat([]byte{0xa5}, 511), 0xa4)
	if freescan.IsSyncPacket(mixed) {
		t.Fatal("expected non-uniform packet to not be a sync packet")
	}
}

func TestIsStatusPacket(t *testing.T) {
	status := []byte{0x10, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0xFF}
	if !freescan.IsStatusPacket(status) {
		t.Fatal("expected STATUS_READY frame to be a status packet")
	}

	pixel := []byte{0x4f, 0x00, 0x51, 0x00, 0x52, 0x00, 0x53, 0x00}
	if freescan.IsStatusPacket(pixel) {
		t.Fatal("expected pixel data to not be a status packet")
	}
}

func TestParsePixels(t *testing.T) {
	raw := []byte{0x4f, 0x00, 0x51, 0x00}
	got := freescan.ParsePixels(raw)
	want := []uint16{79, 81}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestGuessImageDimensions(t *testing.T) {
	cases := []struct {
		pixels       int
		wantW, wantH int
	}{
		{912 * 1424, 912, 1424},
		{806 * 1612, 806, 1612},
		{1003 * 1295, 1003, 1295},
	}

	for _, tc := range cases {
		w, h := freescan.GuessImageDimensions(tc.pixels)
		if w != tc.wantW || h != tc.wantH {
			t.Fatalf("GuessImageDimensions(%d) = %d×%d, want %d×%d", tc.pixels, w, h, tc.wantW, tc.wantH)
		}
	}
}

func TestToGrayImage(t *testing.T) {
	pixels := []uint16{79, 81, 100, 200}
	img := freescan.ToGrayImage(pixels, 2, 2)
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("bounds = %v, want 2×2", img.Bounds())
	}
	want := []byte{79, 81, 100, 200}
	for i, b := range want {
		if img.Pix[i] != b {
			t.Fatalf("pix[%d] = %d, want %d", i, img.Pix[i], b)
		}
	}
}
