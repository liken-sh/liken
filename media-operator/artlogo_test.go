package main

// These tests cover the logo half of the art bridge: the decode of one
// item's logo into the box the display asks for, the cache that answers a
// repeat, and the dispatch that sends each kind of request to its decoder.

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A logo file of one solid color, written where the test can name it.
func writeLogo(t *testing.T, dir, name string, tint color.NRGBA) string {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, tint)
		}
	}
	var buffer bytes.Buffer
	mustSucceed(t, png.Encode(&buffer, source))
	path := filepath.Join(dir, name)
	mustSucceed(t, os.WriteFile(path, buffer.Bytes(), 0o644))
	return path
}

// One item's presentation block naming a logo, the shape the operator
// bakes into the pod.
func logoBlock(logo string) json.RawMessage {
	block, _ := json.Marshal(Presentation{Logo: logo})
	return block
}

// The kind, the path, and the size from one liken-art reply.
func parseLogoReply(t *testing.T, line string) (kind, path string, w, h, stride int) {
	t.Helper()
	var command mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(line), &command))
	args := command.Command
	if len(args) != 8 || args[2] != artReplyMessage {
		t.Fatalf("reply = %q, want a liken-art message", line)
	}
	return args[3].(string), args[4].(string), atoi(t, args[5]), atoi(t, args[6]), atoi(t, args[7])
}

// How long the test waits to be sure the bridge sent nothing.
const artQuietSpell = 100 * time.Millisecond

func expectNoArtReply(t *testing.T, lines <-chan string) {
	t.Helper()
	select {
	case line := <-lines:
		t.Fatalf("the bridge replied %q, want nothing", line)
	case <-time.After(artQuietSpell):
	}
}

// The bridge scales the item's logo into the box the display asked for,
// keeping the aspect ratio, and answers with the file it wrote and that file's
// size.
func TestServeArtDecodesTheItemsLogo(t *testing.T) {
	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{logoBlock(writeLogo(t, t.TempDir(), "logo.png", color.NRGBA{R: 10, G: 200, B: 30, A: 255}))}

	go c.serveArt([]string{artRequestMessage, artKindLogo, "20", "20"})

	kind, path, w, h, stride := parseLogoReply(t, waitForLine(t, lines))
	mustMatch(t, kind, artKindLogo)
	mustMatch(t, w, 20)
	mustMatch(t, h, 10)
	mustMatch(t, stride, 80)
	mustExist(t, path)

	b, g, r := centerPixel(t, path, w, h, stride)
	mustMatch(t, b, byte(30))
	mustMatch(t, g, byte(200))
	mustMatch(t, r, byte(10))
}

// A size already decoded for the current item answers from the cache, so
// the bridge names the file it already wrote and decodes nothing again.
func TestServeLogoAnswersASecondRequestFromTheCache(t *testing.T) {
	logo := writeLogo(t, t.TempDir(), "logo.png", color.NRGBA{B: 255, A: 255})
	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{logoBlock(logo)}
	request := artRequest{kind: artKindLogo, width: 20, height: 20}

	go c.serveLogo(request)
	_, first, _, _, _ := parseLogoReply(t, waitForLine(t, lines))

	mustSucceed(t, os.Remove(logo))
	go c.serveLogo(request)
	_, second, _, _, _ := parseLogoReply(t, waitForLine(t, lines))

	mustMatch(t, second, first)
}

// An https logo the bridge can fetch. The test server signs with its own
// certificate, so the default client the fetch uses trusts it for this test and
// no longer.
func servesLogoOverHTTPS(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transportWas := http.DefaultClient.Transport
	t.Cleanup(func() { http.DefaultClient.Transport = transportWas })
	http.DefaultClient.Transport = server.Client().Transport
	return server.URL + "/logo.png"
}

// A logo of one solid color, encoded the way a fetch returns it.
func encodedLogo(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	mustSucceed(t, png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 10, 10))))
	return buffer.Bytes()
}

// An https logo is a fetch, and the bridge decodes what the fetch
// returns the way it decodes a file the pod mounts.
func TestServeLogoFetchesAnHTTPSLogo(t *testing.T) {
	encoded := encodedLogo(t)
	logo := servesLogoOverHTTPS(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encoded)
	})

	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{logoBlock(logo)}

	go c.serveLogo(artRequest{kind: artKindLogo, width: 10, height: 10})

	_, path, w, h, _ := parseLogoReply(t, waitForLine(t, lines))
	mustMatch(t, w, 10)
	mustMatch(t, h, 10)
	mustExist(t, path)
}

// Every logo the bridge cannot turn into a blob answers nothing, and the
// display then draws its text alone.
func TestServeLogoAnswersNothingWhenItCannotDecode(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, c *commander)
	}{
		{
			name: "the item has no logo",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{json.RawMessage(emptyPresentation)}
			},
		},
		{
			name: "the block is not a presentation",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{json.RawMessage(`"not a block"`)}
			},
		},
		{
			name: "the logo file is not there",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{logoBlock(filepath.Join(t.TempDir(), "gone.png"))}
			},
		},
		{
			name: "the logo file is not an image",
			setup: func(t *testing.T, c *commander) {
				dir := t.TempDir()
				path := filepath.Join(dir, "logo.png")
				mustSucceed(t, os.WriteFile(path, []byte("not an image"), 0o644))
				c.presentations = []json.RawMessage{logoBlock(path)}
			},
		},
		{
			name: "the fetch answers with a status other than 200",
			setup: func(t *testing.T, c *commander) {
				logo := servesLogoOverHTTPS(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				c.presentations = []json.RawMessage{logoBlock(logo)}
			},
		},
		{
			name: "nothing answers the fetch",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{logoBlock("https://127.0.0.1:1/logo.png")}
			},
		},
		{
			name: "the art directory is not there",
			setup: func(t *testing.T, c *commander) {
				c.artDir = filepath.Join(t.TempDir(), "absent")
				c.presentations = []json.RawMessage{logoBlock(writeLogo(t, t.TempDir(), "logo.png", color.NRGBA{A: 255}))}
			},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, lines := bridgeToMPV(t)
			c.artItem = 1
			each.setup(t, c)

			// The call runs on this goroutine because it writes
			// nothing, and a reply it did write would reach the reader
			// the fixture already started.
			c.serveLogo(artRequest{kind: artKindLogo, width: 20, height: 20})

			expectNoArtReply(t, lines)
		})
	}
}

// The playlist can reach the next item while a decode runs. The bridge
// drops that blob and answers nothing, so one item's logo never reaches the
// screen over the next item.
func TestServeLogoDropsABlobTheItemSwapOutran(t *testing.T) {
	encoded := encodedLogo(t)
	fetched := make(chan struct{})
	release := make(chan struct{})
	logo := servesLogoOverHTTPS(t, func(w http.ResponseWriter, r *http.Request) {
		close(fetched)
		<-release
		_, _ = w.Write(encoded)
	})

	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{logoBlock(logo)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.serveLogo(artRequest{kind: artKindLogo, width: 10, height: 10})
	}()

	<-fetched
	c.swapArt(2)
	close(release)
	<-done

	expectNoArtReply(t, lines)
	left, err := os.ReadDir(c.artDir)
	mustSucceed(t, err)
	mustMatch(t, len(left), 0)
}

// serveArt reads the kind and hands the request to the one decoder that
// answers it. A request it cannot parse answers nothing.
func TestServeArtDispatchesByKind(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		setup   func(t *testing.T, c *commander)
		replies bool
		kind    string
	}{
		{
			name: "a logo",
			args: []string{artRequestMessage, artKindLogo, "20", "20"},
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{logoBlock(writeLogo(t, t.TempDir(), "logo.png", color.NRGBA{A: 255}))}
			},
			replies: true,
			kind:    artKindLogo,
		},
		{
			name: "a trickplay tile",
			args: []string{artRequestMessage, artKindTrickplay, "1000", "24", "24"},
			setup: func(t *testing.T, c *commander) {
				t.Setenv(trickplayIntervalVariable, "10s")
				c.presentations = []json.RawMessage{trickBlock(writeSheet(t, map[int]color.RGBA{0: {R: 200, A: 255}}))}
			},
			replies: true,
			kind:    artKindTrickplay,
		},
		{
			name: "an album cover",
			args: []string{artRequestMessage, artKindAlbum, "60", "60"},
			setup: func(t *testing.T, c *commander) {
				c.tracks = []trackEntry{{tier: artTierNone}}
			},
			replies: true,
			kind:    artKindAlbum,
		},
		{
			name:  "a request that does not parse",
			args:  []string{artRequestMessage, artKindLogo, "wide", "20"},
			setup: func(t *testing.T, c *commander) {},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, lines := bridgeToMPV(t)
			c.artItem = 1
			each.setup(t, c)

			if !each.replies {
				c.serveArt(each.args)
				expectNoArtReply(t, lines)
				return
			}
			go c.serveArt(each.args)

			kind, _, _, _, _ := parseLogoReply(t, waitForLine(t, lines))
			mustMatch(t, kind, each.kind)
		})
	}
}

// The playlist reaching a new item drops every blob the last item left,
// so the shared volume holds only what the current item can show and one
// item's art never appears over the next.
func TestSwappingTheItemDropsTheLastItemsArt(t *testing.T) {
	c, _ := bridgeToMPV(t)
	c.artItem = 1
	logo := filepath.Join(c.artDir, "logo-1-20x20.bgra")
	tile := filepath.Join(c.artDir, "trick-1-0-3-24x24.bgra")
	priorTile := filepath.Join(c.artDir, "trick-1-0-2-24x24.bgra")
	for _, path := range []string{logo, tile, priorTile} {
		mustSucceed(t, os.WriteFile(path, []byte("pixels"), 0o644))
	}
	c.artCache = map[string]artBlob{"logo:20x20": {path: logo}}
	c.trickHave, c.trickBlob = true, artBlob{path: tile}
	c.trickHavePrev, c.trickPrev = true, artBlob{path: priorTile}
	c.trickSheetKey = "1:sheets:0"

	c.swapArt(2)

	mustMatch(t, c.artItem, 2)
	mustMatch(t, c.trickHave, false)
	mustMatch(t, c.trickHavePrev, false)
	mustMatch(t, c.trickSheetKey, "")
	mustNotExist(t, logo)
	mustNotExist(t, tile)
	mustNotExist(t, priorTile)
}

// A report that names the item already playing swaps nothing, so the
// blobs the display is showing stay on the volume.
func TestSwappingToTheSameItemKeepsItsArt(t *testing.T) {
	c, _ := bridgeToMPV(t)
	c.artItem = 1
	logo := filepath.Join(c.artDir, "logo-1-20x20.bgra")
	mustSucceed(t, os.WriteFile(logo, []byte("pixels"), 0o644))
	c.artCache = map[string]artBlob{"logo:20x20": {path: logo}}

	c.swapArt(1)

	mustMatch(t, len(c.artCache), 1)
	mustExist(t, logo)
}
