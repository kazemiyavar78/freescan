package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	case "scan":
		runScan(os.Args[2:])
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
  scan      Full scan: wait for trigger, receive image, save output
  help      Show this help message

Scan options:
  --output <path>   PNG output file (default: scan.png)
  --raw <path>      Raw uint16 pixel dump (default: scan.raw)
  --timeout <dur>   Overall scan timeout (default: 120s)
  --width <n>       Image width override when auto-detect fails
  --height <n>      Image height override when auto-detect fails

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

func runScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	output := fs.String("output", "scan.png", "PNG output path")
	rawPath := fs.String("raw", "scan.raw", "raw pixel output path")
	timeout := fs.Duration("timeout", 120*time.Second, "overall scan timeout")
	width := fs.Int("width", 0, "image width override")
	height := fs.Int("height", 0, "image height override")
	fs.Parse(args)

	dev, cleanup := openDevice()
	defer cleanup()

	// لاگ کامل در فایل
	logFile, err := os.OpenFile("scan_debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("[WARN] cannot open log file: %v", err)
	} else {
		defer logFile.Close()
		multiWriter := io.MultiWriter(os.Stderr, logFile)
		log.SetOutput(multiWriter)
		log.Printf("[LOG] Logging to scan_debug.log")
	}

	ctx, cancel := signalContext()
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, *timeout)
	defer cancel()

	// TODO: test whether consecutive scans work without re-init.
	result, err := dev.Scan(ctx)
	if err != nil {
		log.Fatalf("[ERR] %v", err)
	}

	w, h := result.Width, result.Height
	if *width > 0 && *height > 0 {
		w, h = *width, *height
	} else if *width > 0 || *height > 0 {
		log.Fatalf("[ERR] both --width and --height must be set together")
	}

	log.Printf("[IMG] Saving raw to %s...", *rawPath)
	if err := freescan.SaveRaw(result.RawPixels, *rawPath); err != nil {
		log.Fatalf("[ERR] save raw: %v", err)
	}

	img := freescan.ToGrayImage(result.RawPixels, w, h)
	log.Printf("[IMG] Saving to %s...", *output)
	if err := freescan.SavePNG(img, *output); err != nil {
		log.Fatalf("[ERR] save png: %v", err)
	}
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
