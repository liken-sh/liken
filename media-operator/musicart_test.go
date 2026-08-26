package main

// These tests cover the music side of the bridge: the tiers an album's art
// resolves through, and the first segment of an EDL standing for the whole
// album.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/liken-sh/media-operator/edl"
)

// pngBytes encodes one opaque image of the given size, the shape both an
// embedded picture and a cover file take on disk.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			source.Set(x, y, color.NRGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buffer bytes.Buffer
	mustSucceed(t, png.Encode(&buffer, source))
	return buffer.Bytes()
}

// writePNG writes one image beside the media, the way an album's cover file
// sits beside its tracks.
func writePNG(t *testing.T, path string, w, h int) string {
	t.Helper()
	mustSucceed(t, os.WriteFile(path, pngBytes(t, w, h), 0o644))
	return path
}

// id3Frame is one frame of the fixture tag: the four-character name and the
// bytes that follow the frame header.
type id3Frame struct {
	name    string
	payload []byte
}

// textFrame builds one ISO-8859-1 text frame, the encoding byte and the text.
func textFrame(name, value string) id3Frame {
	return id3Frame{name: name, payload: append([]byte{0}, value...)}
}

// pictureFrame builds one APIC frame around a picture: the encoding byte, the
// MIME type, the picture type, an empty description, and the bytes.
func pictureFrame(picture []byte) id3Frame {
	payload := []byte{0}
	payload = append(payload, "image/png"...)
	payload = append(payload, 0, 3, 0)
	return id3Frame{name: "APIC", payload: append(payload, picture...)}
}

// writeMedia writes one media file with an ID3v2.3 tag: the ten-byte header,
// the frames, and a few 0xFF bytes where the audio would start. The tag is the
// whole of what the bridge reads, so the fixture needs no audio.
func writeMedia(t *testing.T, path string, frames ...id3Frame) string {
	t.Helper()
	var body bytes.Buffer
	for _, frame := range frames {
		body.WriteString(frame.name)
		mustSucceed(t, binary.Write(&body, binary.BigEndian, uint32(len(frame.payload))))
		body.Write([]byte{0, 0})
		body.Write(frame.payload)
	}

	var file bytes.Buffer
	file.WriteString("ID3")
	file.Write([]byte{3, 0, 0})
	size := 10 + body.Len()
	file.Write([]byte{
		byte(size>>21) & 0x7f, byte(size>>14) & 0x7f,
		byte(size>>7) & 0x7f, byte(size) & 0x7f,
	})
	file.Write(body.Bytes())
	file.Write(bytes.Repeat([]byte{0xff}, 8))

	mustSucceed(t, os.WriteFile(path, file.Bytes(), 0o644))
	return path
}

// The art tiers settle in one order: the block's own reference, then the
// picture inside the file, then a cover beside it. Each case builds the file
// and its neighbors, so a case states exactly what exists on disk.
func TestTheArtTiersSettleInOrder(t *testing.T) {
	cases := []struct {
		name       string
		block      string
		setup      func(t *testing.T, dir string) string
		wantTier   string
		wantSource func(dir, media string) string
	}{
		{
			name:  "the block's art beats the file and the cover",
			block: `{"art":"https://art.example/cover.png"}`,
			setup: func(t *testing.T, dir string) string {
				writePNG(t, filepath.Join(dir, "cover.jpg"), 8, 8)
				return writeMedia(t, filepath.Join(dir, "track.mp3"), pictureFrame(pngBytes(t, 8, 8)))
			},
			wantTier:   artTierBlock,
			wantSource: func(dir, media string) string { return "https://art.example/cover.png" },
		},
		{
			name:  "the embedded picture beats the cover beside it",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				writePNG(t, filepath.Join(dir, "cover.jpg"), 8, 8)
				return writeMedia(t, filepath.Join(dir, "track.mp3"), pictureFrame(pngBytes(t, 8, 8)))
			},
			wantTier:   artTierEmbedded,
			wantSource: func(dir, media string) string { return media },
		},
		{
			name:  "a cover beside a file that embeds none",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				writePNG(t, filepath.Join(dir, "cover.jpg"), 8, 8)
				return writeMedia(t, filepath.Join(dir, "track.mp3"), textFrame("TIT2", "Track"))
			},
			wantTier:   artTierSibling,
			wantSource: func(dir, media string) string { return filepath.Join(dir, "cover.jpg") },
		},
		{
			name:  "a track with no art anywhere",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				return writeMedia(t, filepath.Join(dir, "track.mp3"), textFrame("TIT2", "Track"))
			},
			wantTier:   artTierNone,
			wantSource: func(dir, media string) string { return "" },
		},
		{
			name:  "a remote track reads no picture and no cover",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				writePNG(t, filepath.Join(dir, "cover.jpg"), 8, 8)
				return "https://media.example/track.mp3"
			},
			wantTier:   artTierNone,
			wantSource: func(dir, media string) string { return "" },
		},
		{
			name:  "a file URI reads the picture the path holds",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				return "file://" + writeMedia(t, filepath.Join(dir, "track.mp3"), pictureFrame(pngBytes(t, 8, 8)))
			},
			wantTier:   artTierEmbedded,
			wantSource: func(dir, media string) string { return filepath.Join(dir, "track.mp3") },
		},
		{
			name:  "an album reads the picture in its first segment",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				writeMedia(t, filepath.Join(dir, "one.mp3"), pictureFrame(pngBytes(t, 8, 8)))
				writeMedia(t, filepath.Join(dir, "two.mp3"), textFrame("TIT2", "Two"))
				return writeEDL(t, filepath.Join(dir, "album.edl"), "one.mp3", "two.mp3")
			},
			wantTier:   artTierEmbedded,
			wantSource: func(dir, media string) string { return filepath.Join(dir, "one.mp3") },
		},
		{
			name:  "an album with no picture reads the cover beside its first segment",
			block: `{}`,
			setup: func(t *testing.T, dir string) string {
				writePNG(t, filepath.Join(dir, "folder.jpg"), 8, 8)
				writeMedia(t, filepath.Join(dir, "one.mp3"), textFrame("TIT2", "One"))
				return writeEDL(t, filepath.Join(dir, "album.edl"), "one.mp3")
			},
			wantTier:   artTierSibling,
			wantSource: func(dir, media string) string { return filepath.Join(dir, "folder.jpg") },
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			media := each.setup(t, dir)
			track := resolveTrack(json.RawMessage(each.block), media)
			mustMatch(t, track.tier, each.wantTier)
			mustMatch(t, track.source, each.wantSource(dir, filepath.Join(dir, "track.mp3")))
		})
	}
}

// writeEDL writes one timeline of segments, each naming a file beside it, the
// shape local/edl prints.
func writeEDL(t *testing.T, path string, files ...string) string {
	t.Helper()
	var text bytes.Buffer
	text.WriteString(edl.Header + "\n")
	for _, file := range files {
		text.WriteString("%" + strconv.Itoa(len(file)) + "%" + file + ",title=%3%One\n")
	}
	mustSucceed(t, os.WriteFile(path, text.Bytes(), 0o644))
	return path
}

// writeCovers writes each named cover file into one directory.
func writeCovers(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		writePNG(t, filepath.Join(dir, name), 4, 4)
	}
}

// The four cover names are tried in one order, so an album that carries more
// than one of them serves the same file every time.
func TestTheSiblingCoverOrder(t *testing.T) {
	cases := []struct {
		name    string
		present []string
		want    string
	}{
		{name: "every name", present: []string{"cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"}, want: "cover.jpg"},
		{name: "no lower-case cover", present: []string{"Cover.jpg", "folder.jpg", "Folder.jpg"}, want: "Cover.jpg"},
		{name: "the two folder names", present: []string{"folder.jpg", "Folder.jpg"}, want: "folder.jpg"},
		{name: "the last name alone", present: []string{"Folder.jpg"}, want: "Folder.jpg"},
		{name: "no cover at all", present: nil, want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCovers(t, dir, each.present...)
			mustMatch(t, siblingCover(filepath.Join(dir, "track.mp3")), coverPath(dir, each.want))
		})
	}
}

// coverPath is the absolute path a wanted cover name takes, and stays empty
// for the case that wants no cover.
func coverPath(dir, name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// mpv's playlist property carries one entry per item, and the bridge reads the
// file name out of each.
func TestParsePlaylistFiles(t *testing.T) {
	data := json.RawMessage(`[{"filename":"/media/1/one.flac","current":true},{"filename":"/media/1/two.flac"}]`)
	files, ok := parsePlaylistFiles(data)
	mustMatch(t, ok, true)
	mustMatchAll(t, files, []string{"/media/1/one.flac", "/media/1/two.flac"})
}

// The picture inside a media file decodes on the same path a cover file takes,
// so an album that embeds its art needs no file beside it.
func TestTheEmbeddedPictureDecodes(t *testing.T) {
	dir := t.TempDir()
	media := writeMedia(t, filepath.Join(dir, "track.mp3"), pictureFrame(pngBytes(t, 20, 20)))

	track := resolveTrack(json.RawMessage(`{}`), media)
	mustMatch(t, track.tier, artTierEmbedded)

	reader, err := openTrackArt(track)
	mustSucceed(t, err)
	defer reader.Close()

	_, w, h, stride, err := scaleToBGRA(reader, 10, 10)
	mustSucceed(t, err)
	mustMatch(t, w, 10)
	mustMatch(t, h, 10)
	mustMatch(t, stride, 40)
}

// A track the bridge found no art for opens nothing, so the display draws its
// text alone rather than reading a broken blob.
func TestOpeningArtForATrackThatHasNone(t *testing.T) {
	_, err := openTrackArt(trackEntry{tier: artTierNone})
	mustFail(t, err)
}

// bridgeToMPV is a commander wired to a socket a test reads, so a reply the
// bridge sends comes back as the line mpv would receive.
func bridgeToMPV(t *testing.T) (*commander, <-chan string) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return &commander{mpv: client, artDir: t.TempDir()}, lines
}

// Every album request the bridge can answer is answered, including the ones
// with nothing to show. Silence reads as a slow decode, so the display asks
// again on every redraw and never stops. An empty path is the answer that ends
// the asking. A request for an item the bridge has not resolved yet is the one
// it holds instead, which TestServeAlbumHoldsARequestUntilThePlaylistResolves
// covers.
func TestServeAlbumAlwaysAnswers(t *testing.T) {
	empty := `{"command":["script-message-to","display","liken-art","album","","0","0","0"]}`

	cases := []struct {
		name  string
		setup func(t *testing.T, c *commander)
	}{
		{
			name: "the item has no art at all",
			setup: func(t *testing.T, c *commander) {
				c.artItem = 1
				c.tracks = []trackEntry{{tier: artTierNone}}
			},
		},
		{
			name: "the art is there but does not decode",
			setup: func(t *testing.T, c *commander) {
				cover := filepath.Join(t.TempDir(), "cover.jpg")
				mustSucceed(t, os.WriteFile(cover, []byte("not an image"), 0o644))
				c.artItem = 1
				c.tracks = []trackEntry{{tier: artTierBlock, source: cover}}
			},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			bridge, lines := bridgeToMPV(t)
			each.setup(t, bridge)

			go bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 600, height: 600})

			select {
			case line := <-lines:
				mustMatch(t, line, empty)
			case <-time.After(time.Second):
				t.Fatal("the bridge answered nothing")
			}
		})
	}
}

// A cover that decodes replies with the blob's own path and size, so the
// empty reply is the answer for nothing to show and not for every request.
func TestServeAlbumAnswersADecodedCover(t *testing.T) {
	bridge, lines := bridgeToMPV(t)
	cover := writePNG(t, filepath.Join(t.TempDir(), "cover.png"), 20, 20)
	bridge.artItem = 1
	bridge.tracks = []trackEntry{{tier: artTierBlock, source: cover}}

	go bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 10, height: 10})

	select {
	case line := <-lines:
		want := `{"command":["script-message-to","display","liken-art","album","` +
			filepath.Join(bridge.artDir, "album-1-10x10.bgra") + `","10","10","40"]}`
		mustMatch(t, line, want)
	case <-time.After(time.Second):
		t.Fatal("the bridge answered nothing")
	}
}

// artReply is the line mpv receives for one answered request, so a test states
// the blob it expects and not the wire shape.
func artReply(path string, w, h, stride int) string {
	return fmt.Sprintf(
		`{"command":["script-message-to","display","liken-art","album","%s","%d","%d","%d"]}`,
		path, w, h, stride)
}

// playlistOf is mpv's playlist property for one file, the shape the reporter
// hands playlistChanged.
func playlistOf(file string) json.RawMessage {
	return json.RawMessage(`[{"filename":` + strconv.Quote(file) + `,"current":true}]`)
}

// The display asks for the cover as soon as it reads the item's block, and mpv
// reports the playing item before it reports the playlist, so the request can
// arrive before the bridge knows what the item's art is. The bridge holds that
// request rather than answering it, because the display asks once and keeps
// the answer, and it serves the held request the moment the playlist resolves.
func TestServeAlbumHoldsARequestUntilThePlaylistResolves(t *testing.T) {
	cases := []struct {
		name  string
		track func(t *testing.T, dir string) string
		want  func(bridge *commander) string
	}{
		{
			name: "the item has a cover",
			track: func(t *testing.T, dir string) string {
				return writeMedia(t, filepath.Join(dir, "track.mp3"), pictureFrame(pngBytes(t, 20, 20)))
			},
			want: func(bridge *commander) string {
				return artReply(filepath.Join(bridge.artDir, "album-1-10x10.bgra"), 10, 10, 40)
			},
		},
		{
			name: "the item has no art at all",
			track: func(t *testing.T, dir string) string {
				return writeMedia(t, filepath.Join(dir, "track.mp3"), textFrame("TIT2", "Track"))
			},
			want: func(bridge *commander) string { return artReply("", 0, 0, 0) },
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			bridge, lines := bridgeToMPV(t)
			bridge.artItem = 1
			file := each.track(t, t.TempDir())

			// The request arrives while the bridge knows the item but not yet
			// the playlist, which is the moment the race opens.
			bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 10, height: 10})
			select {
			case line := <-lines:
				t.Fatalf("the bridge answered %q before it knew the playlist", line)
			case <-time.After(100 * time.Millisecond):
			}

			bridge.playlistChanged(playlistOf(file))

			select {
			case line := <-lines:
				mustMatch(t, line, each.want(bridge))
			case <-time.After(time.Second):
				t.Fatal("the held request was never answered")
			}
			// The held request is served once, so the display places one
			// overlay and the bridge decodes nothing twice.
			select {
			case line := <-lines:
				t.Errorf("the bridge answered twice, the second time %q", line)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

// A request that arrives after the playlist resolved is answered at once, so
// the holding is the exception and not the rule.
func TestServeAlbumAnswersAfterThePlaylistResolved(t *testing.T) {
	bridge, lines := bridgeToMPV(t)
	bridge.artItem = 1
	file := writeMedia(t, filepath.Join(t.TempDir(), "track.mp3"), pictureFrame(pngBytes(t, 20, 20)))

	bridge.playlistChanged(playlistOf(file))
	go bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 10, height: 10})

	select {
	case line := <-lines:
		mustMatch(t, line, artReply(filepath.Join(bridge.artDir, "album-1-10x10.bgra"), 10, 10, 40))
	case <-time.After(time.Second):
		t.Fatal("the bridge answered nothing")
	}
}

// Only the latest box is still on the screen, so a second request replaces the
// one the bridge holds and the playlist answers that one alone.
func TestServeAlbumHoldsTheLatestRequest(t *testing.T) {
	bridge, lines := bridgeToMPV(t)
	bridge.artItem = 1
	file := writeMedia(t, filepath.Join(t.TempDir(), "track.mp3"), pictureFrame(pngBytes(t, 20, 20)))

	bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 10, height: 10})
	bridge.serveAlbum(artRequest{kind: artKindAlbum, width: 20, height: 20})
	bridge.playlistChanged(playlistOf(file))

	select {
	case line := <-lines:
		mustMatch(t, line, artReply(filepath.Join(bridge.artDir, "album-1-20x20.bgra"), 20, 20, 80))
	case <-time.After(time.Second):
		t.Fatal("the held request was never answered")
	}
}
