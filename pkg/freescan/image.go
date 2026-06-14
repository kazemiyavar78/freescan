package freescan

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
)

// SavePNG writes a grayscale image to a PNG file.
func SavePNG(img *image.Gray, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}

// SaveRaw writes uint16 little-endian pixel values to a file for debugging.
func SaveRaw(pixels []uint16, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 2)
	for i, p := range pixels {
		binary.LittleEndian.PutUint16(buf, p)
		if _, err := f.Write(buf); err != nil {
			return fmt.Errorf("write pixel %d: %w", i, err)
		}
	}
	return nil
}
