package main

// A thumbnail is a square JPEG of 256 by 256 pixels at quality 85,
// stored as a data: URI in status.thumbnail. It is small next to the
// 1.5 MiB that etcd accepts for one object.
//
// 256 pixels fill the person picker's 160-pixel circle on a screen
// scaled by 1.6. The JPEG has no alpha channel, and each screen crops it
// to its own shape.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	// Importing a decoder registers its format with image.Decode. With
	// image/jpeg, these add GIF, PNG, and WebP.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/webp"

	scale "golang.org/x/image/draw"
)

const (
	thumbnailSide    = 256
	thumbnailQuality = 85
	thumbnailPrefix  = "data:image/jpeg;base64,"
)

// A Person's spec.avatar can point at any file, and the operator reads
// and decodes it in memory. These limits bound the memory one Person
// can use.
const (
	// maxPictureBytes is the largest encoded file.
	maxPictureBytes = 10 << 20

	// maxPictureSide is the largest width or height in pixels. A small
	// file can declare a huge image, and the decoder allocates the whole
	// canvas: 8192 by 8192 at four bytes a pixel is 256 MiB. The check
	// reads only the header, before decoding.
	maxPictureSide = 8192
)

// errDecode wraps every failure to decode a picture: an unsupported
// format, a corrupt file, or dimensions over maxPictureSide. failure
// (source.go) reports it with the reason DecodeFailed.
var errDecode = errors.New("the picture does not decode")

var errTooLarge = fmt.Errorf("the picture is larger than %d MiB", maxPictureBytes>>20)

// makeThumbnail decodes a picture and returns its thumbnail data: URI.
// The backdrop colour fills the square first, because JPEG has no
// alpha channel and transparent pixels would otherwise encode as black.
func makeThumbnail(picture []byte, backdrop color.Color) (string, error) {
	if len(picture) > maxPictureBytes {
		return "", errTooLarge
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(picture))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecode, err)
	}
	if config.Width > maxPictureSide || config.Height > maxPictureSide {
		return "", fmt.Errorf("%w: it is %dx%d pixels, and the limit is %d pixels on each side",
			errDecode, config.Width, config.Height, maxPictureSide)
	}
	source, _, err := image.Decode(bytes.NewReader(picture))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecode, err)
	}
	return encodeThumbnail(squareScaled(source, backdrop)), nil
}

// squareScaled crops the largest centred square from an image and
// scales it to thumbnailSide. It uses Catmull-Rom, the sharpest scaling
// kernel in golang.org/x/image/draw, to keep detail in faces.
func squareScaled(source image.Image, backdrop color.Color) *image.RGBA {
	bounds := source.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	corner := image.Pt(bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2)
	square := image.Rectangle{Min: corner, Max: corner.Add(image.Pt(side, side))}

	out := image.NewRGBA(image.Rect(0, 0, thumbnailSide, thumbnailSide))
	draw.Draw(out, out.Bounds(), image.NewUniform(backdrop), image.Point{}, draw.Src)
	scale.CatmullRom.Scale(out, out.Bounds(), source, square, scale.Over, nil)
	return out
}

func encodeThumbnail(square image.Image) string {
	return thumbnailURI(encodeJPEG(square))
}

func encodeJPEG(square image.Image) []byte {
	var encoded bytes.Buffer
	// The encoder fails only when its writer fails, and a bytes.Buffer
	// does not.
	_ = jpeg.Encode(&encoded, square, &jpeg.Options{Quality: thumbnailQuality})
	return encoded.Bytes()
}

func thumbnailURI(encoded []byte) string {
	return thumbnailPrefix + base64.StdEncoding.EncodeToString(encoded)
}
