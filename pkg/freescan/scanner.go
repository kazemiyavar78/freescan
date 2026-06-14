package freescan

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"time"
)

const (
	SyncByte          = 0xa5
	SyncLen           = 512
	BulkPacketSize    = 512

	bulkReadTimeout       = 100 * time.Millisecond
	progressPacketStep    = 100
	progressTimeInterval  = 10 * time.Second
	estimatedImageBytes   = 2597888
	estimatedTotalPackets = estimatedImageBytes / BulkPacketSize
)

// ScanResult holds the outcome of a complete scan cycle.
type ScanResult struct {
	RawPixels  []uint16
	Width      int
	Height     int
	TotalBytes int
}

// Scan runs a full scan cycle: wait for the touch trigger, eject the tray,
// receive image data, and send CMD_ACK.
func (d *Device) Scan(ctx context.Context) (*ScanResult, error) {
	if err := d.WaitForScanTrigger(ctx, 0); err != nil {
		return nil, err
	}

	d.log.Printf("[DEV] Sending CMD_OPEN (eject tray for user)")
	cmd := NewCommand(CmdOpen, 0)
	if _, err := d.sendCommand(ctx, cmd); err != nil {
		return nil, fmt.Errorf("scan: eject tray: %w", err)
	}

	d.log.Printf("[DEV] Waiting for image data...")
	raw, err := d.ReceiveImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan: receive image: %w", err)
	}

	pixels := ParsePixels(raw)
	width, height := GuessImageDimensions(len(pixels))

	d.log.Printf("[IMG] Total received: %s bytes (%s pixels)", formatInt(len(raw)), formatInt(len(pixels)))
	d.log.Printf("[IMG] Guessed dimensions: %d × %d", width, height)

	d.log.Printf("[DEV] Sending CMD_ACK")
	if err := d.SendAck(ctx); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return &ScanResult{
		RawPixels:  pixels,
		Width:      width,
		Height:     height,
		TotalBytes: len(raw),
	}, nil
}

// WaitForScanTrigger blocks until STATUS_SCANNING is received (user pressed the touch button).
// A zero timeout uses the context deadline when set, otherwise waits until ctx is cancelled.
func (d *Device) WaitForScanTrigger(ctx context.Context, timeout time.Duration) error {
	d.log.Printf("[DEV] Waiting for scan trigger (touch button on device)...")

	if timeout <= 0 {
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
		} else {
			timeout = 24 * time.Hour
		}
	}

	if err := d.WaitForStatusContext(ctx, StatusScanning, timeout); err != nil {
		return fmt.Errorf("wait for scan trigger: %w", err)
	}
	d.log.Printf("[DEV] Scan triggered! STATUS_SCANNING received")
	return nil
}

// ReceiveImage reads bulk image data from the IN endpoint, skipping sync markers and status packets.
func (d *Device) ReceiveImage(ctx context.Context) ([]byte, error) {
	var buf []byte
	syncFound := false
	packetCount := 0
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("receive image: %w", err)
		}

		packet := make([]byte, BulkPacketSize)
		n, err := d.readBulkPacket(ctx, packet)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return nil, fmt.Errorf("receive image: %w", ctx.Err())
				}
				continue
			}
			if errors.Is(err, context.Canceled) {
				return nil, fmt.Errorf("receive image: %w", err)
			}
			return nil, fmt.Errorf("receive image: bulk read: %w", err)
		}
		if n == 0 {
			continue
		}
		data := packet[:n]

		if !syncFound {
			if IsSyncPacket(data) {
				syncFound = true
				d.log.Printf("[IMG] Sync marker found (%d × 0x%02x)", SyncLen, SyncByte)
				d.log.Printf("[IMG] Receiving pixel data...")
				continue
			}
			if IsStatusPacket(data) {
				continue
			}
			continue
		}

		if IsStatusPacket(data) {
			status := ParseStatusCode(data)
			if status == StatusReady && len(buf) > 0 {
				break
			}
			continue
		}

		buf = append(buf, data...)
		packetCount++

		if packetCount%progressPacketStep == 0 || time.Since(lastProgress) >= progressTimeInterval {
			pct := float64(packetCount) / float64(estimatedTotalPackets) * 100
			if pct > 100 {
				pct = 100
			}
			d.log.Printf("[IMG] Packet %d/%d (%.1f%%)", packetCount, estimatedTotalPackets, pct)
			lastProgress = time.Now()
		}
	}

	if len(buf) == 0 {
		return nil, fmt.Errorf("receive image: no pixel data received")
	}

	d.log.Printf("[IMG] Receiving pixel data... done (%d packets)", packetCount)
	return buf, nil
}

// readBulkPacket reads one bulk IN packet with a short timeout so the caller can poll status.
func (d *Device) readBulkPacket(ctx context.Context, buf []byte) (int, error) {
	readCtx, cancel := context.WithTimeout(ctx, bulkReadTimeout)
	defer cancel()
	return d.inEp.ReadContext(readCtx, buf)
}

// IsSyncPacket reports whether data is the 512-byte image sync marker (all 0xa5).
func IsSyncPacket(data []byte) bool {
	if len(data) < 16 {
		return false
	}
	for _, b := range data {
		if b != SyncByte {
			return false
		}
	}
	return true
}

// IsStatusPacket reports whether data begins with a 12-byte protocol status frame.
func IsStatusPacket(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	return binary.LittleEndian.Uint32(data[4:8]) == FixedWord1
}

// ParseStatusCode extracts the status code from the first 12 bytes of a bulk packet.
func ParseStatusCode(data []byte) uint32 {
	if len(data) < MsgSize {
		return 0
	}
	return Decode(data[:MsgSize]).Code
}

// ParsePixels converts little-endian uint16 raw bytes into a pixel slice.
func ParsePixels(raw []byte) []uint16 {
	n := len(raw) / 2
	pixels := make([]uint16, n)
	for i := 0; i < n; i++ {
		pixels[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return pixels
}

// GuessImageDimensions infers width and height from the pixel count using known IP plate aspect ratios.
// TODO: exact dimensions vary by IP Plate size (0–3); needs hardware testing per size.
func GuessImageDimensions(pixelCount int) (width, height int) {
	ratios := []struct{ w, h int }{
		{912, 1424},
		{768, 1691},
		{1068, 1216},
		{900, 1437},
		{1003, 1295},
		{991, 1311},
		{806, 1612},
	}
	for _, r := range ratios {
		if r.w*r.h == pixelCount {
			return r.w, r.h
		}
	}

	side := int(math.Sqrt(float64(pixelCount)))
	for w := side; w > 0; w-- {
		if pixelCount%w == 0 {
			return w, pixelCount / w
		}
	}
	return 1, pixelCount
}

// ToGrayImage converts uint16 grayscale pixels into a standard Go image.Gray.
// TODO: pixel values may need normalization (observed range 76–85 is narrow).
func ToGrayImage(pixels []uint16, width, height int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, width, height))
	n := width * height
	if len(pixels) < n {
		n = len(pixels)
	}
	for i := 0; i < n; i++ {
		img.Pix[i] = byte(pixels[i] & 0xFF)
	}
	return img
}

func formatInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}

	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}
