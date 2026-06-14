package ftdi

import (
	"fmt"

	"github.com/google/gousb"
)

const (
	reqReset          = 0x00
	reqModemCtrl      = 0x01
	reqGetModemStatus = 0x05
	reqSetBitMode     = 0x0B
	reqReadEEPROM     = 0x90

	resetSIO = 0
	resetIO  = 1

	bitModeSyncFIFO = 0x40
	bitMaskAll      = 0xFF

	eepromWords = 7

	// USB bmRequestType values (vendor, device recipient).
	bmRequestVendorOutDevice = 0x40
	bmRequestVendorInDevice  = 0xC0
)

// Init runs the FT232H initialization sequence captured from the original software:
// EEPROM read, modem control, reset, modem status, then synchronous FIFO bit mode.
func Init(dev *gousb.Device) error {
	for word := 0; word < eepromWords; word++ {
		buf := make([]byte, 2)
		n, err := dev.Control(
			bmRequestVendorInDevice,
			reqReadEEPROM,
			uint16(word),
			0,
			buf,
		)
		if err != nil {
			return fmt.Errorf("ftdi eeprom read word %d: %w", word, err)
		}
		if n != len(buf) {
			return fmt.Errorf("ftdi eeprom read word %d: got %d bytes, want %d", word, n, len(buf))
		}
	}

	for i := 0; i < 2; i++ {
		if err := controlOut(dev, reqModemCtrl, 0x0301, 0, nil); err != nil {
			return fmt.Errorf("ftdi modem ctrl (%d): %w", i+1, err)
		}
	}

	for _, resetType := range []uint16{resetSIO, resetIO, resetSIO} {
		if err := controlOut(dev, reqReset, resetType, 0, nil); err != nil {
			return fmt.Errorf("ftdi reset (type %d): %w", resetType, err)
		}
	}

	status := make([]byte, 2)
	if _, err := dev.Control(
		bmRequestVendorInDevice,
		reqGetModemStatus,
		0,
		0,
		status,
	); err != nil {
		return fmt.Errorf("ftdi get modem status: %w", err)
	}

	// wValue: low byte = mask, high byte = mode
	wValue := uint16(bitMaskAll) | uint16(bitModeSyncFIFO)<<8
	if err := controlOut(dev, reqSetBitMode, wValue, 0, nil); err != nil {
		return fmt.Errorf("ftdi set bit mode: %w", err)
	}

	return nil
}

// controlOut sends a vendor OUT control transfer with no data stage.
func controlOut(dev *gousb.Device, request uint8, value, index uint16, data []byte) error {
	_, err := dev.Control(
		bmRequestVendorOutDevice,
		request,
		value,
		index,
		data,
	)
	return err
}
