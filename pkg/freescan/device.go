package freescan

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/freescan/freescan/internal/ftdi"
	"github.com/google/gousb"
)

const (
	vendorID  = 0x0403
	productID = 0x6014

	endpointIn  = 1 // bulk IN  0x81
	endpointOut = 2 // bulk OUT 0x02

	defaultTimeout      = 2 * time.Second
	defaultPollInterval = 500 * time.Millisecond

	// ftdiPrefixLen: every bulk IN packet from FTDI FT232H starts with 2 status bytes.
	// byte[0] = Modem Status (CTS, DSR, RI, DCD)
	// byte[1] = Line Status  (TX empty, framing error, ...)
	// Application data starts at byte[2].
	ftdiPrefixLen = 2

	// bulkReadBufSize must be larger than MsgSize + ftdiPrefixLen.
	bulkReadBufSize = 512
)

// Logger receives diagnostic output from the device driver.
type Logger interface {
	Printf(format string, args ...any)
}

// Device represents a connected FreeScan scanner over USB.
type Device struct {
	ctx          *gousb.Context
	dev          *gousb.Device
	cfg          *gousb.Config
	intf         *gousb.Interface
	inEp         *gousb.InEndpoint
	outEp        *gousb.OutEndpoint
	timeout      time.Duration
	pollInterval time.Duration
	log          Logger
}

// Option configures optional Device settings.
type Option func(*Device)

// WithTimeout sets the USB transfer timeout.
func WithTimeout(d time.Duration) Option {
	return func(dv *Device) {
		dv.timeout = d
	}
}

// WithPollInterval sets the interval between status polls.
func WithPollInterval(d time.Duration) Option {
	return func(dv *Device) {
		dv.pollInterval = d
	}
}

// WithLogger sets a custom logger; defaults to log.Default().
func WithLogger(l Logger) Option {
	return func(dv *Device) {
		dv.log = l
	}
}

// Open finds the FreeScan device, claims interface 0, initializes FTDI sync FIFO mode,
// and opens bulk endpoints 0x81 (IN) and 0x02 (OUT).
func Open(opts ...Option) (*Device, error) {
	d := &Device{
		timeout:      defaultTimeout,
		pollInterval: defaultPollInterval,
		log:          log.Default(),
	}

	for _, opt := range opts {
		opt(d)
	}

	ctx := gousb.NewContext()
	dev, err := ctx.OpenDeviceWithVIDPID(vendorID, productID)
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("open device %04x:%04x: %w", vendorID, productID, err)
	}
	if dev == nil {
		ctx.Close()
		return nil, fmt.Errorf("device not found (VID=%04x PID=%04x)", vendorID, productID)
	}

	d.ctx = ctx
	d.dev = dev
	d.log.Printf("[USB] Device found: FT232H (%04x:%04x)", vendorID, productID)

	if err := dev.SetAutoDetach(true); err != nil {
		d.log.Printf("[USB] SetAutoDetach not supported (ignored on Windows): %v", err)
	}

	cfg, err := dev.Config(1)
	if err != nil {
		d.closeUSB()
		return nil, fmt.Errorf("get config 1: %w", err)
	}

	intf, err := cfg.Interface(0, 0)
	if err != nil {
		cfg.Close()
		d.closeUSB()
		return nil, fmt.Errorf("claim interface 0: %w", err)
	}
	d.cfg = cfg
	d.intf = intf
	d.log.Printf("[USB] Interface 0 claimed")

	if err := ftdi.Init(dev); err != nil {
		d.Close()
		return nil, fmt.Errorf("ftdi init: %w", err)
	}
	d.log.Printf("[FTDI] SetBitMode: mask=0xFF mode=0x40 (Sync FIFO)")

	inEp, err := intf.InEndpoint(endpointIn)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("open IN endpoint 0x8%x: %w", endpointIn, err)
	}
	outEp, err := intf.OutEndpoint(endpointOut)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("open OUT endpoint 0x0%x: %w", endpointOut, err)
	}

	d.inEp = inEp
	d.outEp = outEp
	// d.inEp.SetTimeout(d.timeout)
	// d.outEp.SetTimeout(d.timeout)

	return d, nil
}

// Close releases USB resources.
func (d *Device) Close() error {
	d.closeUSB()
	return nil
}

func (d *Device) closeUSB() {
	if d.intf != nil {
		d.intf.Close()
		d.intf = nil
	}
	if d.cfg != nil {
		d.cfg.Close()
		d.cfg = nil
	}
	if d.dev != nil {
		d.dev.Close()
		d.dev = nil
	}
	if d.ctx != nil {
		d.ctx.Close()
		d.ctx = nil
	}
}

// sendCommand writes cmd to the OUT endpoint and reads one response,
// stripping the mandatory 2-byte FTDI modem/line-status prefix.
func (d *Device) sendCommand(ctx context.Context, cmd []byte) (Message, error) {
    if err := ctx.Err(); err != nil {
        return Message{}, err
    }

    d.log.Printf("[DEV] Sending command: %s", formatHex(cmd))

    n, err := d.outEp.Write(cmd)
    if err != nil {
        return Message{}, fmt.Errorf("bulk write: %w", err)
    }
    if n != MsgSize {
        return Message{}, fmt.Errorf("bulk write: wrote %d bytes, want %d", n, MsgSize)
    }

    // خواندن response — با retry برای رد کردن ته‌مانده‌های buffer
    for attempt := 0; attempt < 20; attempt++ {
        payload, err := d.readPayload(ctx, 5)
        if err != nil {
            return Message{}, fmt.Errorf("bulk read response: %w", err)
        }
        if len(payload) < MsgSize {
            // ته‌مانده image data — skip و retry
            continue
        }
        // چک کن marker صحیح است (0x00000004)
        if !IsStatusPacket(payload) {
            // image data — skip
            continue
        }
        msg := Decode(payload[:MsgSize])
        d.log.Printf("[DEV] Response: %s (0x%02x) param=0x%08x",
            StatusName(msg.Code), msg.Code, msg.Param)
        return msg, nil
    }
    return Message{}, fmt.Errorf("bulk read response: no valid status after 20 attempts")
}
// readStatus reads a spontaneous status message, stripping the FTDI prefix.
func (d *Device) readStatus(ctx context.Context) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}

	payload, err := d.readPayload(ctx, 5)
	if err != nil {
		return Message{}, fmt.Errorf("read status: %w", err)
	}
	if len(payload) < MsgSize {
		return Message{}, fmt.Errorf("short status: %d bytes", len(payload))
	}

	msg := Decode(payload[:MsgSize])
	d.log.Printf("[DEV] Status: %s (0x%02x)", StatusName(msg.Code), msg.Code)
	return msg, nil
}

// readPayload performs bulk IN reads, stripping the 2-byte FTDI prefix and
// returning the full application payload (up to bulkReadBufSize - ftdiPrefixLen).
// Callers parse status from the first MsgSize bytes when needed.
func (d *Device) readPayload(ctx context.Context, maxRetries int) ([]byte, error) {
	buf := make([]byte, bulkReadBufSize)

	for i := 0; i < maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		n, err := d.inEp.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("bulk read: %w", err)
		}

		if n <= ftdiPrefixLen {
			continue
		}

		payload := make([]byte, n-ftdiPrefixLen)
		copy(payload, buf[ftdiPrefixLen:n])
		return payload, nil
	}

	return nil, fmt.Errorf("no payload received after %d reads", maxRetries)
}

// PollInterval returns the configured polling interval.
func (d *Device) PollInterval() time.Duration {
	return d.pollInterval
}

func formatHex(buf []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(buf)*3-1)
	for i, b := range buf {
		if i > 0 {
			out[i*3-1] = ' '
		}
		out[i*3] = hex[b>>4]
		out[i*3+1] = hex[b&0x0f]
	}
	return string(out)
}

// flushInEp خواندن‌های سریع انجام می‌دهد تا buffer IN endpoint پاک شود.
// این ته‌مانده image data از scan قطع‌شده قبلی را پاک می‌کند.
func (d *Device) flushInEp(ctx context.Context) {
    buf := make([]byte, bulkReadBufSize)
    flushCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
    defer cancel()
    for {
        n, err := d.inEp.ReadContext(flushCtx, buf)
        if err != nil || n == 0 {
            return
        }
    }
}