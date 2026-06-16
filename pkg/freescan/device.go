package freescan

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/freescan/freescan/internal/ftdi"
	"github.com/google/gousb"
)

const (
	endpointIn  = 1 // bulk IN  0x81
	endpointOut = 2 // bulk OUT 0x02

	defaultTimeout      = 2 * time.Second
	defaultPollInterval = 500 * time.Millisecond

	// bulkReadBufSize must be larger than MsgSize + max bulk prefix length.
	bulkReadBufSize = 512
)

// deviceProfile describes USB identity and transport quirks for a supported scanner.
type deviceProfile struct {
	vendorID      uint16
	productID     uint16
	name          string
	needsFTDIInit bool
	bulkPrefixLen int
}

// supportedDevices lists every VID/PID pair the driver can open.
// FTDI FT232H bulk IN packets include a 2-byte modem/line status prefix;
// Cypress FX2LP exposes raw bulk data with no prefix.
var supportedDevices = []deviceProfile{
	{vendorID: 0x0403, productID: 0x6014, name: "FT232H", needsFTDIInit: true, bulkPrefixLen: 2},
	{vendorID: 0x04B4, productID: 0x1004, name: "Cypress FX2LP", needsFTDIInit: false, bulkPrefixLen: 0},
}

// Logger receives diagnostic output from the device driver.
type Logger interface {
	Printf(format string, args ...any)
}

// Device represents a connected FreeScan scanner over USB.
type Device struct {
	ctx           *gousb.Context
	dev           *gousb.Device
	cfg           *gousb.Config
	intf          *gousb.Interface
	inEp          *gousb.InEndpoint
	outEp         *gousb.OutEndpoint
	timeout       time.Duration
	pollInterval  time.Duration
	log           Logger
	profile       deviceProfile
	bulkPrefixLen int
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

// Open finds a supported FreeScan device, claims interface 0, runs chip-specific
// initialization when required, and opens bulk endpoints 0x81 (IN) and 0x02 (OUT).
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
	dev, profile, err := openSupportedDevice(ctx)
	if err != nil {
		ctx.Close()
		return nil, err
	}

	d.ctx = ctx
	d.dev = dev
	d.profile = profile
	d.bulkPrefixLen = profile.bulkPrefixLen
	d.log.Printf("[USB] Device found: %s (%04x:%04x)", profile.name, profile.vendorID, profile.productID)

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

	if profile.needsFTDIInit {
		if err := ftdi.Init(dev); err != nil {
			d.Close()
			return nil, fmt.Errorf("ftdi init: %w", err)
		}
		d.log.Printf("[FTDI] SetBitMode: mask=0xFF mode=0x40 (Sync FIFO)")
	}

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

// openSupportedDevice opens the first connected scanner from supportedDevices.
// Returns the device handle, matched profile, or an error when none is present.
func openSupportedDevice(ctx *gousb.Context) (*gousb.Device, deviceProfile, error) {
	for _, profile := range supportedDevices {
		dev, err := ctx.OpenDeviceWithVIDPID(gousb.ID(profile.vendorID), gousb.ID(profile.productID))
		if err != nil {
			return nil, deviceProfile{}, fmt.Errorf(
				"open device %04x:%04x: %w",
				profile.vendorID,
				profile.productID,
				err,
			)
		}
		if dev != nil {
			return dev, profile, nil
		}
	}

	return nil, deviceProfile{}, fmt.Errorf(
		"device not found (supported: %s)",
		formatSupportedDevices(),
	)
}

// formatSupportedDevices builds a human-readable VID:PID list for error messages.
func formatSupportedDevices() string {
	parts := make([]string, len(supportedDevices))
	for i, profile := range supportedDevices {
		parts[i] = fmt.Sprintf("%04x:%04x", profile.vendorID, profile.productID)
	}
	return strings.Join(parts, ", ")
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

// readPayload performs bulk IN reads, stripping the device-specific bulk prefix and
// returning the full application payload (up to bulkReadBufSize - bulkPrefixLen).
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

		if n <= d.bulkPrefixLen {
			continue
		}

		payload := make([]byte, n-d.bulkPrefixLen)
		copy(payload, buf[d.bulkPrefixLen:n])
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
