package fsthumbnail

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gen2brain/gav1d/avif"
	"github.com/gen2brain/vpx/webp"
)

func TestConvertThumbnailProducesStaticCenterCoverWebP(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 480, 240))
	for y := range 240 {
		for x := range 480 {
			if x < 240 {
				source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				source.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := convertThumbnail("image/png", encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.DecodeAll(bytes.NewReader(thumbnail))
	if err != nil || len(decoded.Image) != 1 || decoded.Image[0].Bounds().Dx() != 160 || decoded.Image[0].Bounds().Dy() != 160 {
		t.Fatalf("thumbnail decode = %#v, %v", decoded, err)
	}
	left := color.NRGBAModel.Convert(decoded.Image[0].At(0, 80)).(color.NRGBA)
	right := color.NRGBAModel.Convert(decoded.Image[0].At(159, 80)).(color.NRGBA)
	if left.R < 150 || right.B < 150 {
		t.Fatalf("cover colors = %#v / %#v", left, right)
	}
}

func TestConvertThumbnailDecodesJPEGAndAnimatedGIFFirstFrame(t *testing.T) {
	jpegSource := image.NewNRGBA(image.Rect(0, 0, 20, 40))
	jpegSource.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, jpegSource, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := convertThumbnail("image/jpeg", jpegBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	first := image.NewPaletted(image.Rect(0, 0, 40, 20), color.Palette{color.Transparent, color.NRGBA{R: 255, A: 255}})
	first.SetColorIndex(0, 0, 1)
	second := image.NewPaletted(first.Bounds(), color.Palette{color.Transparent, color.NRGBA{B: 255, A: 255}})
	second.SetColorIndex(0, 0, 1)
	var gifBytes bytes.Buffer
	if err := gif.EncodeAll(&gifBytes, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{1, 1}, Config: image.Config{Width: 40, Height: 20}}); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := convertThumbnail("image/gif", gifBytes.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.Decode(bytes.NewReader(thumbnail))
	if err != nil {
		t.Fatal(err)
	}
	pixel := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	if pixel.R < pixel.B {
		t.Fatalf("GIF thumbnail used the wrong frame: %#v", pixel)
	}
}

func TestConvertThumbnailDecodesSelectedAVIFAndWebP(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 24, 12))
	source.SetNRGBA(12, 6, color.NRGBA{G: 255, A: 255})
	for _, test := range []struct {
		mime   string
		encode func(*bytes.Buffer) error
	}{
		{mime: "image/avif", encode: func(output *bytes.Buffer) error { return avif.Encode(output, source, avif.EncodeOptions{Quality: 80}) }},
		{mime: "image/webp", encode: func(output *bytes.Buffer) error {
			return webp.Encode(output, source, webp.EncodeOptions{Quality: 80, Method: 4})
		}},
	} {
		t.Run(test.mime, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := test.encode(&encoded); err != nil {
				t.Fatal(err)
			}
			thumbnail, err := convertThumbnail(test.mime, encoded.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := webp.Decode(bytes.NewReader(thumbnail))
			if err != nil || decoded.Bounds().Dx() != 160 || decoded.Bounds().Dy() != 160 {
				t.Fatalf("thumbnail decode = %#v, %v", decoded, err)
			}
		})
	}
}

func TestConvertThumbnailRejectsUnsupportedMalformedAndOversizedInputs(t *testing.T) {
	for _, test := range []struct {
		mime string
		data []byte
		code string
	}{
		{mime: "image/svg+xml", data: []byte("<svg/>"), code: "THUMBNAIL_UNSUPPORTED"},
		{mime: "image/png", data: []byte("not an image"), code: "THUMBNAIL_INVALID"},
		{mime: "image/png", data: make([]byte, maxThumbnailSourceBytes+1), code: "THUMBNAIL_TOO_LARGE"},
	} {
		if _, err := convertThumbnail(test.mime, test.data); !thumbnailErrorIs(err, test.code) {
			t.Fatalf("convertThumbnail(%q) error = %v, want %s", test.mime, err, test.code)
		}
	}
}

func TestConvertThumbnailRejectsOversizedDimensionsBeforeDecode(t *testing.T) {
	data := make([]byte, 8+4+4+13+4)
	copy(data, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(data[8:12], 13)
	copy(data[12:16], "IHDR")
	binary.BigEndian.PutUint32(data[16:20], 10_000)
	binary.BigEndian.PutUint32(data[20:24], 5_001)
	data[24] = 8
	data[25] = 2
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, err := convertThumbnail("image/png", data); !thumbnailErrorIs(err, "THUMBNAIL_TOO_LARGE") {
		t.Fatalf("convertThumbnail() error = %v, want pixel limit", err)
	}
}

func thumbnailErrorIs(err error, code string) bool {
	var thumbnail *Error
	return errors.As(err, &thumbnail) && thumbnail.Code == code
}

func TestJPEGOrientationParserAndTransform(t *testing.T) {
	if got := tiffOrientation([]byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}); got != 6 {
		t.Fatalf("tiffOrientation() = %d", got)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	oriented := orientThumbnailImage(source, 6)
	if oriented.Bounds().Dx() != 3 || oriented.Bounds().Dy() != 2 || color.NRGBAModel.Convert(oriented.At(2, 0)).(color.NRGBA).R != 255 {
		t.Fatalf("orientation 6 result is incorrect")
	}
}
