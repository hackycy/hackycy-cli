package fsthumbnail

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"github.com/gen2brain/gav1d/avif"
	"github.com/gen2brain/vpx/webp"
	xdraw "golang.org/x/image/draw"
)

const (
	// MaxSourceBytes bounds a single thumbnail conversion request.
	MaxSourceBytes          = 64 << 20
	maxThumbnailSourceBytes = MaxSourceBytes
	maxThumbnailPixels      = 50_000_000
	thumbnailDimension      = 160
	thumbnailQuality        = 72
)

// Error reports a thumbnail runtime failure without exposing FS command types.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (err *Error) Error() string {
	return err.Message
}

func (err *Error) Unwrap() error {
	return err.Cause
}

// Convert validates and converts an image into the thumbnail wire format.
func Convert(mimeType string, source []byte) ([]byte, error) {
	return convertThumbnail(mimeType, source)
}

// Validate checks source limits and image metadata before worker dispatch.
func Validate(mimeType string, source []byte) error {
	return validateThumbnailSource(mimeType, source)
}

func convertThumbnail(mimeType string, source []byte) ([]byte, error) {
	if err := validateThumbnailSource(mimeType, source); err != nil {
		return nil, err
	}
	decoded, err := decodeThumbnailImage(mimeType, source)
	if err != nil {
		var thumbnail *Error
		if errors.As(err, &thumbnail) {
			return nil, err
		}
		return nil, &Error{Code: "THUMBNAIL_INVALID", Message: "Image could not be converted into a thumbnail", Cause: err}
	}
	result := image.NewNRGBA(image.Rect(0, 0, thumbnailDimension, thumbnailDimension))
	fillTransparent(result)
	coverThumbnail(result, decoded)
	var output bytes.Buffer
	if err := webp.Encode(&output, result, webp.EncodeOptions{Quality: thumbnailQuality, Method: 4}); err != nil {
		return nil, &Error{Code: "THUMBNAIL_INVALID", Message: "Image could not be converted into a thumbnail", Cause: err}
	}
	return output.Bytes(), nil
}

func validateThumbnailSource(mimeType string, source []byte) error {
	if len(source) > maxThumbnailSourceBytes {
		return &Error{Code: "THUMBNAIL_TOO_LARGE", Message: "Thumbnail source exceeds the 64 MiB limit"}
	}
	config, err := thumbnailImageConfig(mimeType, source)
	if err != nil {
		var thumbnail *Error
		if errors.As(err, &thumbnail) {
			return err
		}
		return &Error{Code: "THUMBNAIL_INVALID", Message: "Image could not be converted into a thumbnail", Cause: err}
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxThumbnailPixels {
		return &Error{Code: "THUMBNAIL_TOO_LARGE", Message: "Thumbnail image exceeds the 50,000,000 pixel limit"}
	}
	return nil
}

func decodeThumbnailImage(mimeType string, source []byte) (image.Image, error) {
	reader := func() *bytes.Reader { return bytes.NewReader(source) }
	if _, err := thumbnailImageConfig(mimeType, source); err != nil {
		return nil, err
	}
	var result image.Image
	var err error
	switch strings.ToLower(mimeType) {
	case "image/avif":
		sequence, decodeErr := avif.DecodeAll(reader(), avif.Options{AutoRotate: false, FrameSizeLimit: maxThumbnailPixels})
		err = decodeErr
		if err == nil && len(sequence.Image) > 0 {
			result = sequence.Image[0]
		}
	case "image/webp":
		sequence, decodeErr := webp.DecodeAll(reader(), webp.Options{AutoRotate: true, FrameSizeLimit: maxThumbnailPixels})
		err = decodeErr
		if err == nil && len(sequence.Image) > 0 {
			result = sequence.Image[0]
		}
	case "image/gif":
		animation, decodeErr := gif.DecodeAll(reader())
		err = decodeErr
		if err == nil && len(animation.Image) > 0 {
			canvas := image.NewNRGBA(image.Rect(0, 0, animation.Config.Width, animation.Config.Height))
			draw.Draw(canvas, animation.Image[0].Bounds(), animation.Image[0], animation.Image[0].Bounds().Min, draw.Over)
			result = canvas
		}
	case "image/jpeg":
		result, err = jpeg.Decode(reader())
		if err == nil {
			result = orientThumbnailImage(result, jpegOrientation(source))
		}
	case "image/png":
		result, err = png.Decode(reader())
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("thumbnail decoder returned no image")
	}
	return result, nil
}

func thumbnailImageConfig(mimeType string, source []byte) (image.Config, error) {
	reader := func() *bytes.Reader { return bytes.NewReader(source) }
	switch strings.ToLower(mimeType) {
	case "image/avif":
		return avif.DecodeConfig(reader())
	case "image/webp":
		return webp.DecodeConfig(reader())
	case "image/gif":
		return gif.DecodeConfig(reader())
	case "image/jpeg":
		return jpeg.DecodeConfig(reader())
	case "image/png":
		return png.DecodeConfig(reader())
	default:
		return image.Config{}, &Error{Code: "THUMBNAIL_UNSUPPORTED", Message: "Thumbnail format is not supported"}
	}
}

func coverThumbnail(destination *image.NRGBA, source image.Image) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := math.Max(float64(thumbnailDimension)/float64(width), float64(thumbnailDimension)/float64(height))
	targetWidth := int(math.Ceil(float64(width) * scale))
	targetHeight := int(math.Ceil(float64(height) * scale))
	target := image.Rect((thumbnailDimension-targetWidth)/2, (thumbnailDimension-targetHeight)/2, (thumbnailDimension-targetWidth)/2+targetWidth, (thumbnailDimension-targetHeight)/2+targetHeight)
	xdraw.ApproxBiLinear.Scale(destination, target, source, bounds, draw.Over, nil)
}

func fillTransparent(image *image.NRGBA) {
	for index := range image.Pix {
		image.Pix[index] = 0
	}
}

func jpegOrientation(source []byte) int {
	for offset := 2; offset+4 <= len(source) && source[offset] == 0xff; {
		marker := source[offset+1]
		offset += 2
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if offset+2 > len(source) {
			break
		}
		length := int(binary.BigEndian.Uint16(source[offset:]))
		if length < 2 || offset+length > len(source) {
			break
		}
		if marker == 0xe1 && length >= 10 && bytes.Equal(source[offset+2:offset+8], []byte("Exif\x00\x00")) {
			if orientation := tiffOrientation(source[offset+8 : offset+length]); orientation != 0 {
				return orientation
			}
		}
		offset += length
	}
	return 1
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(data[2:]) != 42 {
		return 0
	}
	offset := int(order.Uint32(data[4:]))
	if offset < 0 || offset+2 > len(data) {
		return 0
	}
	count := int(order.Uint16(data[offset:]))
	for index := 0; index < count; index++ {
		entry := offset + 2 + index*12
		if entry+12 > len(data) {
			return 0
		}
		if order.Uint16(data[entry:]) != 0x0112 || order.Uint16(data[entry+2:]) != 3 || order.Uint32(data[entry+4:]) != 1 {
			continue
		}
		orientation := int(order.Uint16(data[entry+8:]))
		if orientation >= 1 && orientation <= 8 {
			return orientation
		}
	}
	return 0
}

func orientThumbnailImage(source image.Image, orientation int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if orientation <= 1 || orientation > 8 {
		return source
	}
	targetWidth, targetHeight := width, height
	if orientation >= 5 {
		targetWidth, targetHeight = height, width
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			destinationX, destinationY := orientCoordinate(x, y, width, height, orientation)
			target.SetNRGBA(destinationX, destinationY, color.NRGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return target
}

func orientCoordinate(x, y, width, height, orientation int) (int, int) {
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
		return height - 1 - y, x
	case 7:
		return height - 1 - y, width - 1 - x
	case 8:
		return y, width - 1 - x
	default:
		return x, y
	}
}
