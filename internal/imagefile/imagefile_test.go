package imagefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestNormalizeFitsAndStripsPNGMetadata(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1024, 256))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	withMetadata := insertPNGChunk(t, encoded.Bytes(), "tEXt", []byte("private\x00metadata"))
	normalized, err := Normalize(bytes.NewReader(withMetadata))
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(normalized))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Width != 512 || configuration.Height != 128 {
		t.Fatalf("normalized dimensions = %dx%d", configuration.Width, configuration.Height)
	}
	if bytes.Contains(normalized, []byte("private")) || bytes.Contains(normalized, []byte("tEXt")) {
		t.Fatal("normalized PNG retained source metadata")
	}
}

func TestNormalizeAppliesJPEGOrientation(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := range 10 {
		for x := range 20 {
			value := color.NRGBA{R: 255, A: 255}
			if x >= 10 {
				value = color.NRGBA{B: 255, A: 255}
			}
			source.SetNRGBA(x, y, value)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	oriented := append([]byte{}, encoded.Bytes()[:2]...)
	oriented = append(oriented, exifOrientationSegment(6)...)
	oriented = append(oriented, encoded.Bytes()[2:]...)
	normalized, err := Normalize(bytes.NewReader(oriented))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(normalized))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 10 || decoded.Bounds().Dy() != 20 {
		t.Fatalf("oriented dimensions = %v", decoded.Bounds())
	}
	upperR, _, upperB, _ := decoded.At(5, 2).RGBA()
	lowerR, _, lowerB, _ := decoded.At(5, 17).RGBA()
	if upperR <= upperB || lowerB <= lowerR {
		t.Fatal("JPEG orientation did not rotate pixels clockwise")
	}
}

func TestNormalizeRejectsUntrustedFormsAndBounds(t *testing.T) {
	valid := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, valid); err != nil {
		t.Fatal(err)
	}
	animated := insertPNGChunk(t, encoded.Bytes(), "acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0})
	for name, value := range map[string][]byte{
		"wrong signature": []byte("not an image"),
		"animated PNG":    animated,
		"malformed PNG":   encoded.Bytes()[:len(encoded.Bytes())-1],
		"too many bytes":  make([]byte, MaxSourceBytes+1),
		"dimension bomb":  pngDimensions(t, encoded.Bytes(), MaxDimension+1, 1),
		"pixel bomb":      pngDimensions(t, encoded.Bytes(), 7000, 7000),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize(bytes.NewReader(value)); !errorsIsInvalid(err) {
				t.Fatalf("Normalize error = %v", err)
			}
		})
	}
}

func pngDimensions(t *testing.T, encoded []byte, width, height uint32) []byte {
	t.Helper()
	result := append([]byte{}, encoded...)
	if string(result[12:16]) != "IHDR" {
		t.Fatal("PNG does not begin with IHDR")
	}
	binary.BigEndian.PutUint32(result[16:20], width)
	binary.BigEndian.PutUint32(result[20:24], height)
	binary.BigEndian.PutUint32(result[29:33], crc32.ChecksumIEEE(result[12:29]))
	return result
}

func insertPNGChunk(t *testing.T, encoded []byte, kind string, data []byte) []byte {
	t.Helper()
	position := bytes.LastIndex(encoded, []byte("IEND")) - 4
	if position < 8 {
		t.Fatal("PNG has no IEND chunk")
	}
	chunk := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(data)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], data)
	binary.BigEndian.PutUint32(chunk[8+len(data):], crc32.ChecksumIEEE(chunk[4:8+len(data)]))
	result := append([]byte{}, encoded[:position]...)
	result = append(result, chunk...)
	return append(result, encoded[position:]...)
}

func exifOrientationSegment(value uint16) []byte {
	tiff := make([]byte, 26)
	copy(tiff, "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], value)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	return append(segment, payload...)
}

func errorsIsInvalid(err error) bool { return errors.Is(err, ErrInvalid) }
