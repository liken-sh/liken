package main

// A thumbnail is one square JPEG, 256 by 256 pixels, at quality 85,
// as a data: URI. At that size a face takes about 20 to 35 KB after
// base64, a small part of the 1.5 MiB that etcd allows one object, and
// it covers the person picker's 160-pixel circle on a screen that
// scales by up to 1.6. The thumbnail is square and has no alpha, and
// each screen crops it to its own shape.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	// The decoders register themselves with the image package. These
	// four formats are the ones Go decodes with the standard library
	// and golang.org/x/image.
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

// The limits on one picture. A source can name any file, and the
// operator reads it into memory, so each limit bounds what one Person
// can cost the process.
const (
	// maxPictureBytes bounds the encoded file.
	maxPictureBytes = 10 << 20

	// maxPictureSide bounds each side of the decoded image. A small
	// file can declare a huge image, and the decoder allocates the
	// whole canvas: 8192 by 8192 at four bytes a pixel is 256 MiB. The
	// check reads only the header, before any pixel is decoded.
	maxPictureSide = 8192
)

// errDecode marks a picture the operator cannot turn into pixels: a
// format it does not read, a corrupt file, or an image too large to
// decode. The condition reports it as DecodeFailed, apart from a
// source it could not read.
var errDecode = errors.New("the picture does not decode")

// errTooLarge is a file over maxPictureBytes.
var errTooLarge = fmt.Errorf("the picture is larger than %d MiB", maxPictureBytes>>20)

// bakeThumbnail decodes one picture and answers its thumbnail. The
// person's colour fills the square first, so the transparent parts of
// a PNG, a GIF, or a WebP show that colour and not black.
func bakeThumbnail(picture []byte, backdrop color.Color) (string, error) {
	if len(picture) > maxPictureBytes {
		return "", errTooLarge
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(picture))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecode, err)
	}
	if config.Width > maxPictureSide || config.Height > maxPictureSide {
		return "", fmt.Errorf("%w: it is %dx%d pixels, over the limit of %d on each side",
			errDecode, config.Width, config.Height, maxPictureSide)
	}
	source, _, err := image.Decode(bytes.NewReader(picture))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecode, err)
	}
	return encodeThumbnail(squareScaled(source, backdrop)), nil
}

// squareScaled crops the centre square out of an image and scales it
// to the thumbnail's side. Catmull-Rom is the sharpest of the kernels
// that golang.org/x/image/draw offers, and a face at 256 pixels needs
// the detail.
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

// encodeThumbnail encodes a square as the data: URI that
// status.thumbnail holds.
func encodeThumbnail(square image.Image) string {
	return thumbnailURI(encodeJPEG(square))
}

func encodeJPEG(square image.Image) []byte {
	var encoded bytes.Buffer
	// The encoder fails only when its writer fails, and a bytes.Buffer
	// never fails a write.
	_ = jpeg.Encode(&encoded, square, &jpeg.Options{Quality: thumbnailQuality})
	return encoded.Bytes()
}

func thumbnailURI(encoded []byte) string {
	return thumbnailPrefix + base64.StdEncoding.EncodeToString(encoded)
}
