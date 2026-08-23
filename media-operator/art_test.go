package main

// These tests cover the decode side the bridge and the standalone
// serve-art mode share: a request parses into a kind and a box, and a
// known image scales into premultiplied bgra of the right shape.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestParseArtRequest(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		request artRequest
		ok      bool
	}{
		{name: "a logo request", args: []string{"liken-art-request", "logo", "280", "96"}, request: artRequest{kind: "logo", width: 280, height: 96}, ok: true},
		{name: "a trickplay request", args: []string{"liken-art-request", "trickplay", "125000", "240", "136"}, request: artRequest{kind: "trickplay", timeMs: 125000, width: 240, height: 136}, ok: true},
		{name: "another script's broadcast", args: []string{"someone-else", "logo", "280", "96"}},
		{name: "an unknown kind", args: []string{"liken-art-request", "poster", "280", "96"}},
		{name: "too few arguments", args: []string{"liken-art-request", "logo", "280"}},
		{name: "a trickplay with no box", args: []string{"liken-art-request", "trickplay", "125000", "240"}},
		{name: "a trickplay time that is not a number", args: []string{"liken-art-request", "trickplay", "soon", "240", "136"}},
		{name: "a width that is not a number", args: []string{"liken-art-request", "logo", "wide", "96"}},
		{name: "a height of zero", args: []string{"liken-art-request", "logo", "280", "0"}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			request, ok := parseArtRequest(each.args)
			mustMatch(t, ok, each.ok)
			mustMatch(t, request, each.request)
		})
	}
}

// A 100x40 image scaled into a 50x50 box keeps its 5:2 aspect, so it
// comes out 50x20 with a stride of four times the width.
func TestScaleToBGRAFitsTheBoxAndKeepsAspect(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 100, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			source.Set(x, y, color.NRGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	var buffer bytes.Buffer
	mustSucceed(t, png.Encode(&buffer, source))

	pixels, w, h, stride, err := scaleToBGRA(&buffer, 50, 50)
	mustSucceed(t, err)
	mustMatch(t, w, 50)
	mustMatch(t, h, 20)
	mustMatch(t, stride, 200)
	mustMatch(t, len(pixels), stride*h)

	// A center pixel is the opaque green, in bgra byte order.
	offset := 10*stride + 25*4
	mustMatch(t, pixels[offset+0], byte(30))  // blue
	mustMatch(t, pixels[offset+1], byte(200)) // green
	mustMatch(t, pixels[offset+2], byte(10))  // red
	mustMatch(t, pixels[offset+3], byte(255)) // alpha
}

// A fully transparent pixel decodes to all zeros, because overlay-add
// reads premultiplied alpha and Go's image package premultiplies.
func TestScaleToBGRAPremultipliesTransparency(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			source.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 0})
		}
	}
	var buffer bytes.Buffer
	mustSucceed(t, png.Encode(&buffer, source))

	pixels, _, _, _, err := scaleToBGRA(&buffer, 8, 8)
	mustSucceed(t, err)
	for index, value := range pixels {
		if value != 0 {
			t.Fatalf("byte %d = %d, want 0 for a transparent image", index, value)
		}
	}
}

// A reader that is not an image fails rather than writing a blob.
func TestScaleToBGRARefusesGarbage(t *testing.T) {
	_, _, _, _, err := scaleToBGRA(bytes.NewBufferString("not an image"), 10, 10)
	mustFail(t, err)
}
