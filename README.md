# FreeScan USB Driver

Go driver for the **FreeScan Imaging Plate Scanner**, communicating over USB with an FT232H chip in synchronous FIFO mode.

## Requirements

- Go 1.21+
- [libusb](https://libusb.info/) — on Windows, place `libusb-1.0.dll` on your `PATH`
- **Zadig** — replace the default FTDI CDM driver with **WinUSB** (or libusb-win32) for device `0403:6014`

> Windows ships an FTDI virtual COM port driver that conflicts with direct libusb access. Use [Zadig](https://zadig.akeo.ie/) to bind WinUSB to the FreeScan device before running this tool.

## Build

On Windows you need **libusb development headers** and **pkg-config** (e.g. via [MSYS2](https://www.msys2.org/): `pacman -S mingw-w64-x86_64-libusb mingw-w64-x86_64-pkg-config`). Place `libusb-1.0.dll` on your `PATH` at runtime.

```bash
go build -o freescan.exe ./cmd/freescan
```

## Usage

```bash
freescan connect   # Connect and show device status
freescan poll      # Poll current status
freescan open      # Eject / open the imaging plate tray
freescan close     # Close the tray
```

Example output:

```
[USB] Device found: FT232H (0403:6014)
[USB] Interface 0 claimed
[FTDI] SetBitMode: mask=0xFF mode=0x40 (Sync FIFO)
[DEV] Sending CMD_OPEN: 02 00 00 00 04 00 00 00 00 00 00 00
[DEV] Response: STATUS_READY (0x10)
[DEV] Tray is opening...
[DEV] Response: STATUS_BUSY (0x12)
[DEV] Tray open complete
```

## Project layout

```
freescan/
├── cmd/freescan/       CLI entry point
├── internal/ftdi/      FTDI control-transfer initialization
├── pkg/freescan/       Protocol, device, and command API
└── go.mod
```

## Protocol

12-byte little-endian frames:

| Offset | Field    | Description              |
|--------|----------|--------------------------|
| 0–3    | Code     | Command or status code   |
| 4–7    | Marker   | Always `0x00000004`      |
| 8–11   | Param    | Parameter or status data |

Commands: `CMD_POLL` (0x01), `CMD_OPEN` (0x02), `CMD_CLOSE` (0x03)

Status: `STATUS_READY` (0x10), `STATUS_BUSY` (0x12)

## Tests

```bash
go test ./...
```

Unit tests cover protocol encode/decode only. Integration tests require the physical device.

## Limitations

- Tray open/close and status polling are implemented.
- Image scan/acquisition is **not** implemented yet (`// TODO: scan phase` in code).

## License

See repository license.
