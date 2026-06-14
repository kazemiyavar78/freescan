package freescan

import (
	"context"
	"fmt"
	"time"
)

// Poll sends CMD_POLL and returns the device status code from the response.
func (d *Device) Poll() (uint32, error) {
	return d.PollContext(context.Background())
}

// PollContext sends CMD_POLL and returns the status code, honouring ctx cancellation.
func (d *Device) PollContext(ctx context.Context) (uint32, error) {
	cmd := NewCommand(CmdPoll, PollParam)
	msg, err := d.sendCommand(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("poll: %w", err)
	}
	return msg.Code, nil
}

// OpenTray sends CMD_OPEN and waits until STATUS_BUSY indicates the tray is moving.
func (d *Device) OpenTray() error {
	return d.OpenTrayContext(context.Background(), 30*time.Second)
}

// OpenTrayContext sends CMD_OPEN and waits for STATUS_BUSY with ctx/timeout support.
func (d *Device) OpenTrayContext(ctx context.Context, timeout time.Duration) error {
	cmd := NewCommand(CmdOpen, 0)
	d.log.Printf("[DEV] Sending CMD_OPEN: %s", formatHex(cmd))

	msg, err := d.sendCommand(ctx, cmd)
	if err != nil {
		return fmt.Errorf("open tray command: %w", err)
	}
	if msg.Code != StatusReady {
		return fmt.Errorf("open tray: expected immediate STATUS_READY, got %s (0x%02x)", StatusName(msg.Code), msg.Code)
	}

	d.log.Printf("[DEV] Tray is opening...")

	if err := d.WaitForStatusContext(ctx, StatusBusy, timeout); err != nil {
		return fmt.Errorf("open tray: %w", err)
	}

	d.log.Printf("[DEV] Tray open complete")
	return nil
}

// CloseTray sends CMD_CLOSE and waits until STATUS_READY indicates the tray is closed.
func (d *Device) CloseTray() error {
	return d.CloseTrayContext(context.Background(), 30*time.Second)
}

// CloseTrayContext sends CMD_CLOSE and waits for STATUS_READY with ctx/timeout support.
func (d *Device) CloseTrayContext(ctx context.Context, _ time.Duration) error {
	cmd := NewCommand(CmdClose, 0)
	d.log.Printf("[DEV] Sending CMD_CLOSE: %s", formatHex(cmd))

	msg, err := d.sendCommand(ctx, cmd)
	if err != nil {
		return fmt.Errorf("close tray command: %w", err)
	}
	if msg.Code != StatusReady {
		return fmt.Errorf("close tray: expected STATUS_READY, got %s (0x%02x)", StatusName(msg.Code), msg.Code)
	}

	d.log.Printf("[DEV] Tray close complete")
	return nil
}

// WaitForStatus polls until the expected status is received or timeout expires.
func (d *Device) WaitForStatus(expected uint32, timeout time.Duration) error {
	return d.WaitForStatusContext(context.Background(), expected, timeout)
}

// WaitForStatusContext polls until expected status, honouring ctx cancellation.
func (d *Device) WaitForStatusContext(ctx context.Context, expected uint32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	tryRead := func() (bool, error) {
		status, err := d.readStatus(ctx)
		if err != nil {
			code, pollErr := d.PollContext(ctx)
			if pollErr != nil {
				return false, err
			}
			if code == expected {
				return true, nil
			}
			return false, nil
		}
		if status.Code == expected {
			return true, nil
		}
		return false, nil
	}

	if ok, err := tryRead(); err != nil {
		return fmt.Errorf("wait for %s: read status: %w", StatusName(expected), err)
	} else if ok {
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for %s: %w", StatusName(expected), err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for %s: timeout after %s", StatusName(expected), timeout)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", StatusName(expected), ctx.Err())
		case <-ticker.C:
			ok, err := tryRead()
			if err != nil {
				return fmt.Errorf("wait for %s: read status: %w", StatusName(expected), err)
			}
			if ok {
				return nil
			}
		}
	}
}

// TODO: scan phase — image acquisition not yet implemented.
