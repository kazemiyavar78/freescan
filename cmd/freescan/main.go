package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/freescan/freescan/pkg/freescan"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "connect":
		runConnect()
	case "open":
		runOpen()
	case "close":
		runClose()
	case "poll":
		runPoll()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `FreeScan USB driver CLI

Usage:
  freescan <command>

Commands:
  connect   Connect to the device and show current status
  open      Open the imaging plate tray
  close     Close the imaging plate tray
  poll      Poll and display current device status
  help      Show this help message

Requirements:
  - libusb-1.0.dll on PATH (Windows)
  - WinUSB driver installed via Zadig (VID 0403, PID 6014)
`)
}

func openDevice() (*freescan.Device, func()) {
	dev, err := freescan.Open(freescan.WithLogger(log.Default()))
	if err != nil {
		log.Fatalf("[ERR] %v", err)
	}
	cleanup := func() {
		if err := dev.Close(); err != nil {
			log.Printf("[ERR] close: %v", err)
		}
	}
	return dev, cleanup
}

func runConnect() {
	dev, cleanup := openDevice()
	defer cleanup()

	ctx, cancel := signalContext()
	defer cancel()

	status, err := dev.PollContext(ctx)
	if err != nil {
		log.Fatalf("[ERR] poll: %v", err)
	}

	log.Printf("[DEV] Connected — status: %s (0x%02x)", freescan.StatusName(status), status)
}

func runOpen() {
	dev, cleanup := openDevice()
	defer cleanup()

	ctx, cancel := signalContext()
	defer cancel()

	if err := dev.OpenTrayContext(ctx, 30*time.Second); err != nil {
		log.Fatalf("[ERR] %v", err)
	}
}

func runClose() {
	dev, cleanup := openDevice()
	defer cleanup()

	ctx, cancel := signalContext()
	defer cancel()

	if err := dev.CloseTrayContext(ctx, 30*time.Second); err != nil {
		log.Fatalf("[ERR] %v", err)
	}
}

func runPoll() {
	dev, cleanup := openDevice()
	defer cleanup()

	ctx, cancel := signalContext()
	defer cancel()

	status, err := dev.PollContext(ctx)
	if err != nil {
		log.Fatalf("[ERR] poll: %v", err)
	}

	log.Printf("[DEV] Status: %s (0x%02x)", freescan.StatusName(status), status)
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
