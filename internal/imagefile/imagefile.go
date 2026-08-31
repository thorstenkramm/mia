// Package imagefile normalizes untrusted avatar and logo images.
package imagefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"

	_ "image/jpeg"
)

const (
	MaxSourceBytes = 10 << 20
	MaxDimension   = 10_000
	MaxPixels      = 40_000_000
	MaxOutputSide  = 512
)

var ErrInvalid = errors.New("invalid image")

// Normalize validates JPEG or PNG bytes and returns one metadata-free PNG.
func Normalize(source io.Reader) ([]byte, error) {
	encoded, err := io.ReadAll(io.LimitReader(source, MaxSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(encoded) == 0 || len(encoded) > MaxSourceBytes {
		return nil, ErrInvalid
	}
	format := ""
	orientation := 1
	switch {
	case bytes.HasPrefix(encoded, []byte{0xff, 0xd8, 0xff}):
		format = "jpeg"
		orientation, err = jpegOrientation(encoded)
	case bytes.HasPrefix(encoded, []byte("\x89PNG\r\n\x1a\n")):
		format = "png"
		err = validatePNG(encoded)
	default:
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, ErrInvalid
	}
	configuration, detected, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || detected != format || configuration.Width < 1 || configuration.Height < 1 ||
		configuration.Width > MaxDimension || configuration.Height > MaxDimension ||
		uint64(configuration.Width)*uint64(configuration.Height) > MaxPixels {
		return nil, ErrInvalid
	}
	decoded, detected, err := image.Decode(bytes.NewReader(encoded))
	if err != nil || detected != format {
		return nil, ErrInvalid
	}
	normalized := orientAndFit(decoded, orientation)
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&output, normalized); err != nil {
		return nil, fmt.Errorf("encode normalized PNG: %w", err)
	}
	return output.Bytes(), nil
}

func orientAndFit(source image.Image, orientation int) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	orientedWidth, orientedHeight := width, height
	if orientation >= 5 {
		orientedWidth, orientedHeight = height, width
	}
	outputWidth, outputHeight := orientedWidth, orientedHeight
	if outputWidth > MaxOutputSide || outputHeight > MaxOutputSide {
		if outputWidth >= outputHeight {
			outputWidth = MaxOutputSide
			outputHeight = max(1, orientedHeight*MaxOutputSide/orientedWidth)
		} else {
			outputHeight = MaxOutputSide
			outputWidth = max(1, orientedWidth*MaxOutputSide/orientedHeight)
		}
	}
	output := image.NewNRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	for y := range outputHeight {
		orientedY := min(orientedHeight-1, y*orientedHeight/outputHeight)
		for x := range outputWidth {
			orientedX := min(orientedWidth-1, x*orientedWidth/outputWidth)
			sourceX, sourceY := inverseOrientation(orientedX, orientedY, width, height, orientation)
			value, ok := color.NRGBAModel.Convert(source.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY)).(color.NRGBA)
			if !ok {
				panic("color.NRGBAModel returned a non-NRGBA color")
			}
			output.SetNRGBA(x, y, value)
		}
	}
	return output
}

func inverseOrientation(x, y, width, height, orientation int) (int, int) {
	switch orientation {
	case 2:
		return width - 1 - x, y
	case 3:
		return width - 1 - x, height - 1 - y
	case 4:
		return x, height - 1 - y
	case 5:
		return y, x
	case 6:
		return y, height - 1 - x
	case 7:
		return width - 1 - y, height - 1 - x
	case 8:
		return width - 1 - y, x
	default:
		return x, y
	}
}

// validatePNG walks the bounded chunk stream and rejects APNG before decoding.
func validatePNG(encoded []byte) error {
	position := 8
	seenEnd := false
	for position+12 <= len(encoded) {
		length := int(binary.BigEndian.Uint32(encoded[position : position+4]))
		if length < 0 || length > len(encoded)-position-12 {
			return ErrInvalid
		}
		chunkType := string(encoded[position+4 : position+8])
		if chunkType == "acTL" || chunkType == "fcTL" || chunkType == "fdAT" {
			return ErrInvalid
		}
		position += length + 12
		if chunkType == "IEND" {
			seenEnd = true
			break
		}
	}
	if !seenEnd || position != len(encoded) {
		return ErrInvalid
	}
	return nil
}

func jpegOrientation(encoded []byte) (int, error) {
	position := 2
	for position+4 <= len(encoded) {
		if encoded[position] != 0xff {
			return 0, ErrInvalid
		}
		for position < len(encoded) && encoded[position] == 0xff {
			position++
		}
		if position >= len(encoded) {
			return 0, ErrInvalid
		}
		marker := encoded[position]
		position++
		if marker == 0xda || marker == 0xd9 {
			return 1, nil
		}
		if marker >= 0xd0 && marker <= 0xd7 || marker == 0x01 {
			continue
		}
		if position+2 > len(encoded) {
			return 0, ErrInvalid
		}
		length := int(binary.BigEndian.Uint16(encoded[position : position+2]))
		if length < 2 || position+length > len(encoded) {
			return 0, ErrInvalid
		}
		payload := encoded[position+2 : position+length]
		position += length
		if marker == 0xe1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			return exifOrientation(payload[6:])
		}
	}
	return 1, nil
}

func exifOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 0, ErrInvalid
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, ErrInvalid
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0, ErrInvalid
	}
	offset := uint64(order.Uint32(tiff[4:8]))
	if offset+2 > uint64(len(tiff)) {
		return 0, ErrInvalid
	}
	entries := uint64(order.Uint16(tiff[offset : offset+2]))
	position := offset + 2
	if entries > (uint64(len(tiff))-position)/12 {
		return 0, ErrInvalid
	}
	for entry := uint64(0); entry < entries; entry++ {
		item := tiff[position+entry*12 : position+(entry+1)*12]
		if order.Uint16(item[:2]) != 0x0112 {
			continue
		}
		if order.Uint16(item[2:4]) != 3 || order.Uint32(item[4:8]) != 1 {
			return 0, ErrInvalid
		}
		orientation := int(order.Uint16(item[8:10]))
		if orientation < 1 || orientation > 8 {
			return 0, ErrInvalid
		}
		return orientation, nil
	}
	return 1, nil
}
