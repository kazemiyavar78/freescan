package freescan

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"time"
)

const (
	SyncByte       = 0xa5
	SyncLen        = 512
	BulkPacketSize = 512

	bulkReadTimeout     = 100 * time.Millisecond
	imageIdleTimeout    = 2 * time.Second
	progressInterval    = 2 * time.Second
	estimatedImageBytes = 2_597_888
)

// ScanResult holds the outcome of a complete scan cycle.
type ScanResult struct {
	RawPixels  []uint16
	Width      int
	Height     int
	TotalBytes int
}

// Scan runs a full scan cycle: open tray, wait for touch trigger, receive image, send ACK.
func (d *Device) Scan(ctx context.Context) (*ScanResult, error) {
	d.flushInEp(ctx)

	d.log.Printf("[DEV] Opening tray...")
	if err := d.OpenTrayContext(ctx, 30*time.Second); err != nil {
		return nil, fmt.Errorf("scan: open tray: %w", err)
	}

	d.log.Printf("[DEV] Place film, then press touch button...")
	if err := d.WaitForScanTrigger(ctx, 0); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	d.log.Printf("[DEV] Receiving image...")
	raw, err := d.ReceiveImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan: receive image: %w", err)
	}

	pixels := ParsePixels(raw)
	width, height := GuessImageDimensions(len(pixels))
	d.log.Printf("[IMG] %d bytes, %d pixels, %dx%d", len(raw), len(pixels), width, height)

	if err := d.SendAck(ctx); err != nil {
		d.log.Printf("[DEV] ACK warning: %v", err)
	}

	return &ScanResult{
		RawPixels:  pixels,
		Width:      width,
		Height:     height,
		TotalBytes: len(raw),
	}, nil
}

// WaitForScanTrigger فقط CMD_POLL می‌فرستد و منتظر STATUS_SCANNING می‌ماند.
// از WaitForStatusContext استفاده نمی‌کند چون آن تابع ممکن است
// CMD_POLL را وسط image stream بفرستد و transfer را خراب کند.
func (d *Device) WaitForScanTrigger(ctx context.Context, timeout time.Duration) error {
	d.log.Printf("[DEV] Waiting for scan trigger (touch button on device)...")

	if timeout <= 0 {
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
		} else {
			timeout = 24 * time.Hour
		}
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for scan trigger: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for scan trigger: timeout")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for scan trigger: %w", ctx.Err())
		case <-ticker.C:
			// فقط CMD_POLL می‌فرستیم - هیچ readStatus یا readPayload اضافه نیست
			code, err := d.PollContext(ctx)
			if err != nil {
				// خطا در poll را ignore کن، دوباره تلاش کن
				continue
			}
			if code == StatusScanning {
				d.log.Printf("[DEV] Scan triggered! STATUS_SCANNING received")
				return nil
			}
		}
	}
}

// ReceiveImage reads bulk image data from the IN endpoint, skipping sync markers and status packets.
// CMD_POLL is sent in the main loop when no data arrives until the sync marker is found.
// End detection uses STATUS_READY, expected byte count, or idle time without pixel data.
func (d *Device) ReceiveImage(ctx context.Context) ([]byte, error) {
	var buf []byte
	syncFound := false
	packetCount := 0
	startTime := time.Now()
	lastImageDataTime := time.Now()
	lastLogTime := time.Now()
	lastPollTime := time.Now().Add(-d.pollInterval)

	d.log.Printf("[IMG] Waiting for sync marker...")

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("receive image: %w", err)
		}

		packet := make([]byte, BulkPacketSize)
		n, err := d.readBulkPacket(ctx, packet)
		if err != nil {
			return nil, fmt.Errorf("receive image: %w", err)
		}

		if n == 0 {
			if !syncFound {
				if time.Since(lastPollTime) >= d.pollInterval {
					cmd := NewCommand(CmdPoll, PollParam)
					d.outEp.Write(cmd)
					lastPollTime = time.Now()
					d.log.Printf("[IMG] Polling... (%ds)", int(time.Since(startTime).Seconds()))
				}
				continue
			}
			if time.Since(lastImageDataTime) > imageIdleTimeout {
				d.log.Printf("[IMG] No data for %v — complete", imageIdleTimeout)
				break
			}
			continue
		}

		data := packet[:n]

		if packetCount < 20 {
			d.log.Printf(
				"[DBG] packet=%d len=%d first=% x",
				packetCount,
				len(data),
				data[:min(16,len(data))],
			)
		}

		if IsStatusPacket(data) {
			code := ParseStatusCode(data)
			if syncFound && code == StatusReady {
				d.log.Printf("[IMG] STATUS_READY — complete")
				break
			}
			continue
		}

		if !syncFound {
			if IsSyncPacket(data) {
				syncFound = true
				lastImageDataTime = time.Now()
				d.log.Printf("[IMG] Sync marker found — receiving pixels...")
			}
			continue
		}

		buf = append(buf, data...)
		packetCount++
		lastImageDataTime = time.Now()

		if len(buf) >= estimatedImageBytes {
			d.log.Printf("[IMG] Expected bytes received (%d)", len(buf))
			break
		}

		if time.Since(lastLogTime) >= progressInterval {
			elapsed := time.Since(startTime)
			pct := float64(len(buf)) / float64(estimatedImageBytes) * 100
			if pct > 100 {
				pct = 100
			}
			d.log.Printf("[IMG] %d / %d bytes (%.1f%%) — %.0fs",
				len(buf), estimatedImageBytes, pct, elapsed.Seconds())
			lastLogTime = time.Now()
		}
	}

	if len(buf) == 0 {
		return nil, fmt.Errorf("receive image: no pixel data received")
	}

	elapsed := time.Since(startTime)
	d.log.Printf("[IMG] Complete: %d bytes in %.1fs (%d packets)",
		len(buf), elapsed.Seconds(), packetCount)
	return buf, nil
}

// readBulkPacket reads one bulk IN transfer, strips the device-specific bulk prefix,
// and returns the application payload. Returns (0, nil) when only prefix/status
// bytes arrive or the per-read timeout expires without data.
func (d *Device) readBulkPacket(ctx context.Context, buf []byte) (int, error) {
	readCtx, cancel := context.WithTimeout(ctx, bulkReadTimeout)
	defer cancel()

	tmp := make([]byte, bulkReadBufSize)
	n, err := d.inEp.ReadContext(readCtx, tmp)
	if err != nil {
		if readCtx.Err() != nil && ctx.Err() == nil {
			return 0, nil
		}
		return 0, err
	}

	if n <= d.bulkPrefixLen {
		return 0, nil
	}

	payload := tmp[d.bulkPrefixLen:n]
	copy(buf, payload)
	return len(payload), nil
}

func IsSyncPacket(data []byte) bool {

    count := 0

    for _, b := range data {
        if b == SyncByte {
            count++
        }
    }

    return count > len(data)/2
}

// IsSyncPacket reports whether data is the 512-byte image sync marker (all 0xa5).
// func IsSyncPacket(data []byte) bool {
// 	if len(data) < 16 {
// 		return false
// 	}
// 	for _, b := range data {
// 		if b != SyncByte {
// 			return false
// 		}
// 	}
// 	return true
// }

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

func min(a,b int) int {
    if a < b {
        return a
    }
    return b
}