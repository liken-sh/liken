package main

// The bridge decodes the art the display cannot. overlay-add takes raw bgra,
// and mpv's Lua has no image decoder. So the display asks the bridge for a
// logo at a pixel size. The bridge reads the file, scales it, writes the bgra
// to the volume it shares with mpv, and answers with the path and the size.
// The scaler is a small bilinear resampler on the standard library, so the
// decoder needs no dependency and no cgo.

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// One decoded logo, ready for overlay-add: the file the bridge wrote, and the
// size mpv needs to read it back.
type artBlob struct {
	path   string
	width  int
	height int
	stride int
}

// One parsed art request. timeMs carries the scrub time for a trickplay
// request, and stays zero for a logo request, which needs only the box.
type artRequest struct {
	kind   string
	timeMs int
	width  int
	height int
}

// parseArtRequest reads a client-message as a request for art. The first
// argument names the request, so the bridge drops another script's broadcast.
// The rest depends on the kind: a logo carries a box, a trickplay carries a
// time and a box.
func parseArtRequest(args []string) (artRequest, bool) {
	if len(args) < 4 || args[0] != artRequestMessage {
		return artRequest{}, false
	}
	switch args[1] {
	case artKindLogo:
		w, h, ok := parseBox(args[2], args[3])
		if !ok {
			return artRequest{}, false
		}
		return artRequest{kind: artKindLogo, width: w, height: h}, true
	case artKindTrickplay:
		if len(args) < 5 {
			return artRequest{}, false
		}
		timeMs, err := strconv.Atoi(args[2])
		if err != nil || timeMs < 0 {
			return artRequest{}, false
		}
		w, h, ok := parseBox(args[3], args[4])
		if !ok {
			return artRequest{}, false
		}
		return artRequest{kind: artKindTrickplay, timeMs: timeMs, width: w, height: h}, true
	}
	return artRequest{}, false
}

// parseBox reads a width and a height from two arguments. A value that is not
// a positive number fails the request, so a bad box decodes nothing.
func parseBox(widthArg, heightArg string) (w, h int, ok bool) {
	w, err := strconv.Atoi(widthArg)
	if err != nil || w <= 0 {
		return 0, 0, false
	}
	h, err = strconv.Atoi(heightArg)
	if err != nil || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// serveArt answers one art request. It dispatches by kind, the logo to
// serveLogo and the trickplay tile to serveTrickplay.
func (c *commander) serveArt(args []string) {
	request, ok := parseArtRequest(args)
	if !ok {
		return
	}
	switch request.kind {
	case artKindLogo:
		c.serveLogo(request)
	case artKindTrickplay:
		c.serveTrickplay(request)
	}
}

// serveLogo decodes the current item's logo to the box the display asks for,
// caches the blob by size, and replies. A size already decoded for the current
// item replies from the cache and decodes nothing.
func (c *commander) serveLogo(request artRequest) {
	kind, w, h := request.kind, request.width, request.height
	key := kind + ":" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
	c.artMutex.Lock()
	item := c.artItem
	if blob, cached := c.artCache[key]; cached {
		c.artMutex.Unlock()
		c.replyArt(kind, blob)
		return
	}
	block := c.blockForItem(item)
	c.artMutex.Unlock()

	logo := logoOf(block)
	if logo == "" {
		return
	}

	blob, err := c.decodeLogo(item, logo, w, h)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: logo %q: %v\n", logo, err)
		return
	}

	c.artMutex.Lock()
	if item != c.artItem {
		// The item swapped while the decode ran. Drop this blob, so the last
		// item's art does not stay on the shared volume.
		c.artMutex.Unlock()
		os.Remove(blob.path)
		return
	}
	if c.artCache == nil {
		c.artCache = map[string]artBlob{}
	}
	c.artCache[key] = blob
	c.artMutex.Unlock()

	c.replyArt(kind, blob)
}

// decodeLogo reads one logo, scales it into the box, and writes the bgra to
// the shared volume. An https logo is fetched. Any other value is a path the
// resolver rewrote under the media mount.
func (c *commander) decodeLogo(item int, logo string, w, h int) (artBlob, error) {
	reader, err := openArt(logo)
	if err != nil {
		return artBlob{}, err
	}
	defer reader.Close()

	pixels, outW, outH, stride, err := scaleToBGRA(reader, w, h)
	if err != nil {
		return artBlob{}, err
	}

	path := filepath.Join(c.artDir, fmt.Sprintf("logo-%d-%dx%d.bgra", item, w, h))
	if err := os.WriteFile(path, pixels, 0o644); err != nil {
		return artBlob{}, err
	}
	return artBlob{path: path, width: outW, height: outH, stride: stride}, nil
}

// openArt opens a logo for reading. An https logo is a network fetch. Any
// other value is a file the pod mounts.
func openArt(logo string) (io.ReadCloser, error) {
	if strings.HasPrefix(logo, "https://") {
		response, err := http.Get(logo)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("fetch returned %s", response.Status)
		}
		return response.Body, nil
	}
	return os.Open(logo)
}

// replyArt hands the display one ready blob over the mpv socket. The display
// registers artReplyMessage and places the blob with overlay-add.
func (c *commander) replyArt(kind string, blob artBlob) {
	c.command([]any{
		"script-message-to", displayClientName, artReplyMessage,
		kind, blob.path,
		strconv.Itoa(blob.width), strconv.Itoa(blob.height), strconv.Itoa(blob.stride),
	})
}

// logoOf reads the logo path from one presentation block. An empty block, or
// one with no logo, has none.
func logoOf(block []byte) string {
	var presentation Presentation
	if err := json.Unmarshal(block, &presentation); err != nil {
		return ""
	}
	return presentation.Logo
}

// scaleToBGRA decodes an image and scales it to fit within boxW by boxH,
// keeping the aspect ratio. It returns premultiplied bgra with stride 4*w, the
// one format overlay-add reads. Go's image package returns alpha-premultiplied
// color, and blending in that space keeps a transparent edge clean.
func scaleToBGRA(reader io.Reader, boxW, boxH int) (pixels []byte, w, h, stride int, err error) {
	source, _, err := image.Decode(reader)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return scaleRegionToBGRA(source, source.Bounds(), boxW, boxH)
}

// scaleRegionToBGRA scales one rectangle of a decoded image to fit within boxW
// by boxH, keeping the aspect ratio. It returns premultiplied bgra with stride
// 4*w, the one format overlay-add reads. A logo passes the image's full bounds.
// A trickplay passes one cell of a sprite sheet, so the crop and the scale are
// one step.
func scaleRegionToBGRA(source image.Image, region image.Rectangle, boxW, boxH int) (pixels []byte, w, h, stride int, err error) {
	sw, sh := region.Dx(), region.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, 0, 0, 0, fmt.Errorf("the image has no pixels")
	}

	scale := math.Min(float64(boxW)/float64(sw), float64(boxH)/float64(sh))
	outW := max(1, int(math.Round(float64(sw)*scale)))
	outH := max(1, int(math.Round(float64(sh)*scale)))
	stride = 4 * outW
	pixels = make([]byte, stride*outH)

	for y := 0; y < outH; y++ {
		// The half-pixel offset samples each output pixel at its own center,
		// so the scaled image does not drift half a pixel.
		fy := (float64(y)+0.5)/scale - 0.5
		for x := 0; x < outW; x++ {
			fx := (float64(x)+0.5)/scale - 0.5
			r, g, b, a := sampleBilinear(source, region, fx, fy)
			offset := y*stride + x*4
			pixels[offset+0] = b
			pixels[offset+1] = g
			pixels[offset+2] = r
			pixels[offset+3] = a
		}
	}
	return pixels, outW, outH, stride, nil
}

// sampleBilinear reads the four source pixels around (fx, fy) and blends them,
// so a scaled edge is smooth. The coordinates are in the source's own pixels,
// measured from its bounds origin.
func sampleBilinear(source image.Image, bounds image.Rectangle, fx, fy float64) (r, g, b, a byte) {
	x0 := int(math.Floor(fx))
	y0 := int(math.Floor(fy))
	dx := fx - float64(x0)
	dy := fy - float64(y0)

	r00, g00, b00, a00 := sampleClamped(source, bounds, x0, y0)
	r10, g10, b10, a10 := sampleClamped(source, bounds, x0+1, y0)
	r01, g01, b01, a01 := sampleClamped(source, bounds, x0, y0+1)
	r11, g11, b11, a11 := sampleClamped(source, bounds, x0+1, y0+1)

	blend := func(v00, v10, v01, v11 float64) byte {
		top := v00 + (v10-v00)*dx
		bottom := v01 + (v11-v01)*dx
		value := top + (bottom-top)*dy
		// Go returns each channel in 0..65535, so the high byte is the 8-bit
		// value overlay-add reads.
		return byte(uint32(value+0.5) >> 8)
	}
	return blend(r00, r10, r01, r11),
		blend(g00, g10, g01, g11),
		blend(b00, b10, b01, b11),
		blend(a00, a10, a01, a11)
}

// sampleClamped reads one source pixel. It clamps to the edge for a coordinate
// the blend reaches past the image. The channels are the alpha-premultiplied
// values the image package gives, each 0..65535.
func sampleClamped(source image.Image, bounds image.Rectangle, x, y int) (r, g, b, a float64) {
	if x < 0 {
		x = 0
	}
	if x > bounds.Dx()-1 {
		x = bounds.Dx() - 1
	}
	if y < 0 {
		y = 0
	}
	if y > bounds.Dy()-1 {
		y = bounds.Dy() - 1
	}
	ri, gi, bi, ai := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
	return float64(ri), float64(gi), float64(bi), float64(ai)
}
