package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

// encodeAs encodes a solid 40 by 30 picture as jpeg, gif, or png.
func encodeAs(t *testing.T, format string, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			img.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&out, img, nil)
	case "gif":
		err = gif.Encode(&out, img, nil)
	default:
		err = png.Encode(&out, img)
	}
	if err != nil {
		t.Fatalf("encoding the test picture: %v", err)
	}
	return out.Bytes()
}

// The operator reads JPEG, PNG, GIF, and WebP, and each produces a
// 256-pixel square JPEG.
func TestEachFormatMakesASquareThumbnail(t *testing.T) {
	webp, err := os.ReadFile("testdata/picture.webp")
	if err != nil {
		t.Fatalf("reading the WebP picture: %v", err)
	}
	cases := map[string][]byte{
		"jpeg": encodeAs(t, "jpeg", green),
		"png":  encodeAs(t, "png", green),
		"gif":  encodeAs(t, "gif", green),
		"webp": webp,
	}
	for name, picture := range cases {
		t.Run(name, func(t *testing.T) {
			thumbnail, err := makeThumbnail(picture, blue)
			if err != nil {
				t.Fatalf("making the thumbnail of the %s picture: %v", name, err)
			}
			if bounds := decodeThumbnail(t, thumbnail).Bounds(); bounds.Dx() != thumbnailSide || bounds.Dy() != thumbnailSide {
				t.Errorf("the thumbnail is %v, want %dx%d", bounds, thumbnailSide, thumbnailSide)
			}
		})
	}
}

func TestAWidePictureKeepsItsCentre(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 100))
	for y := range 100 {
		for x := range 300 {
			img.Set(x, y, []color.RGBA{red, green, blue}[x/100])
		}
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, img); err != nil {
		t.Fatalf("encoding the three-band picture: %v", err)
	}

	thumbnail, err := makeThumbnail(picture.Bytes(), blue)
	if err != nil {
		t.Fatalf("making the thumbnail of the three-band picture: %v", err)
	}
	if got := centre(t, thumbnail); !near(got, green) {
		t.Errorf("the centre is %v, want the green middle third", got)
	}
}

func TestATransparentPictureShowsTheBackdrop(t *testing.T) {
	thumbnail, err := makeThumbnail(solid(t, 32, 32, color.RGBA{}), red)
	if err != nil {
		t.Fatalf("making the thumbnail of the transparent picture: %v", err)
	}
	if got := centre(t, thumbnail); !near(got, red) {
		t.Errorf("the centre is %v, want the red backdrop", got)
	}
}

// A file over either limit, and a file that does not decode, fail with
// the error that tells them apart.
func TestThePictureLimits(t *testing.T) {
	truncated := solid(t, 64, 64, green)
	cases := []struct {
		name    string
		picture []byte
		want    error
	}{
		{"a file over 10 MiB", make([]byte, maxPictureBytes+1), errTooLarge},
		{"a picture wider than 8192 pixels", solid(t, maxPictureSide+1, 1, green), errDecode},
		{"a picture taller than 8192 pixels", solid(t, 1, maxPictureSide+1, green), errDecode},
		{"a file in no format the operator reads", []byte("BM not a picture"), errDecode},
		{"a picture cut short", truncated[:len(truncated)/2], errDecode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := makeThumbnail(c.picture, blue); !errors.Is(err, c.want) {
				t.Errorf("makeThumbnail error = %v, want one that wraps %v", err, c.want)
			}
		})
	}
}

// A picture exactly at the side limit still produces a thumbnail.
func TestAPictureAtTheLimitMakesAThumbnail(t *testing.T) {
	if _, err := makeThumbnail(solid(t, maxPictureSide, 1, green), blue); err != nil {
		t.Errorf("makeThumbnail of a picture %d pixels wide returned %v, want a thumbnail", maxPictureSide, err)
	}
}
