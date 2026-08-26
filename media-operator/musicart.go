package main

// The music art pipeline. An album's cover lives inside its media files as
// often as it lives in the Play's spec, so the bridge resolves each item's
// art through tiers and reads a file's tags where the block is silent. An
// album arrives as one EDL item, and its first segment carries the cover
// for the whole timeline. The tiers are settled once per playlist, when mpv
// reports it, so no art request opens a media file again.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
	"github.com/liken-sh/media-operator/edl"
)

// The tiers an item's art resolves through, in the order the bridge tries
// them. A reference the block states beats the file, because the block
// carries what the operator resolved from the spec. The picture inside the
// file beats a cover beside it, because a file's own art is more specific
// than its folder's.
const (
	artTierNone     = ""
	artTierBlock    = "block"
	artTierEmbedded = "embedded"
	artTierSibling  = "sibling"
)

// The sibling cover file names, in the order the bridge tries them. Rippers
// and library managers write one cover file into an album's folder, so one
// file serves every track in it.
var coverFileNames = []string{"cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"}

// One item's art as the bridge resolved it: the tier it came from, and the
// file that holds it.
type trackEntry struct {
	tier   string
	source string
}

// resolveTrack settles one item's art from its presentation block and the
// file mpv named in the playlist. An album is one EDL item, so the file
// tiers read the album's first segment, and one album resolves one cover.
func resolveTrack(block json.RawMessage, file string) trackEntry {
	var presentation Presentation
	if err := json.Unmarshal(block, &presentation); err != nil {
		presentation = Presentation{}
	}
	path := albumSourcePath(file)
	tier, source := resolveTrackArt(presentation, path, readTags(path))
	return trackEntry{tier: tier, source: source}
}

// resolveTrackArt is the tier order itself: the block's own reference, then
// the picture inside the file, then a cover beside it. An item that reaches
// the end of the list draws no art.
func resolveTrackArt(presentation Presentation, path string, metadata tag.Metadata) (tier, source string) {
	if presentation.Art != "" {
		return artTierBlock, presentation.Art
	}
	if metadata != nil && metadata.Picture() != nil && len(metadata.Picture().Data) > 0 {
		return artTierEmbedded, path
	}
	if cover := siblingCover(path); cover != "" {
		return artTierSibling, cover
	}
	return artTierNone, ""
}

// albumSourcePath is the file the two file tiers read. A plain media file
// is its own source. An EDL is a timeline of tracks, so the album's first
// segment stands for the whole of it.
func albumSourcePath(file string) string {
	text, dir, ok := albumTimeline(file)
	if !ok {
		return localMediaPath(file)
	}
	segments := edl.Parse(text)
	if len(segments) == 0 {
		return ""
	}
	return edl.SegmentPath(dir, segments[0].File)
}

// albumTimeline returns the timeline an item names, and the directory a
// relative segment path is measured from. mpv reads a timeline from a file
// the shim wrote or from an edl:// URL, where a semicolon stands in for the
// newline. An item that names no timeline says so, and the caller reads it
// as a plain media file.
func albumTimeline(file string) (text, dir string, ok bool) {
	if strings.HasPrefix(file, edlScheme) {
		inline := strings.TrimPrefix(file, edlScheme)
		return strings.ReplaceAll(inline, edlURLSeparator, "\n"), "", true
	}
	path := localMediaPath(file)
	if path == "" || !strings.EqualFold(filepath.Ext(path), edlFileExtension) {
		return "", "", false
	}
	text, ok = edl.ReadFile(path)
	if !ok {
		return "", "", false
	}
	return text, filepath.Dir(path), true
}

// The two ways an item names a timeline, and the separator that stands in
// for a newline inside the URL form.
const (
	edlScheme        = "edl://"
	edlURLSeparator  = ";"
	edlFileExtension = ".edl"
)

// readTags reads one file's tags, and reads nothing for an item that is not
// a local file. A file with no tags is not an error here: it is an item
// whose art comes from the block, from a cover beside it, or from nowhere.
func readTags(path string) tag.Metadata {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	metadata, err := tag.ReadFrom(file)
	if err != nil {
		return nil
	}
	return metadata
}

// localMediaPath turns one playlist entry into a path the bridge can open,
// and returns nothing for a URI it cannot read as a file. The tags and the
// sibling cover both need a local file.
func localMediaPath(entry string) string {
	path := strings.TrimPrefix(entry, "file://")
	if strings.Contains(path, "://") {
		return ""
	}
	return path
}

// siblingCover finds the album's own cover file beside the track.
func siblingCover(path string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	for _, name := range coverFileNames {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// openTrackArt opens the art the tier named. The block's reference and the
// sibling cover are files or URLs the logo path already opens, and the
// embedded picture comes out of the media file itself.
func openTrackArt(track trackEntry) (io.ReadCloser, error) {
	switch track.tier {
	case artTierBlock, artTierSibling:
		return openArt(track.source)
	case artTierEmbedded:
		return openEmbeddedPicture(track.source)
	}
	return nil, fmt.Errorf("the item has no art")
}

// openEmbeddedPicture reads the picture out of one media file's tags and
// hands it back as bytes, so the decode path is the same one a file on disk
// takes.
func openEmbeddedPicture(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	metadata, err := tag.ReadFrom(file)
	if err != nil {
		return nil, err
	}
	picture := metadata.Picture()
	if picture == nil || len(picture.Data) == 0 {
		return nil, fmt.Errorf("the file embeds no picture")
	}
	return io.NopCloser(bytes.NewReader(picture.Data)), nil
}

// One entry of mpv's playlist property. The bridge reads the file name
// alone: the entry's other fields say what plays now, and the bridge already
// observes that through playlist-pos.
type mpvPlaylistEntry struct {
	Filename string `json:"filename"`
}

// parsePlaylistFiles reads mpv's playlist property into the file names in
// playlist order.
func parsePlaylistFiles(data json.RawMessage) ([]string, bool) {
	var entries []mpvPlaylistEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false
	}
	files := make([]string, len(entries))
	for index, entry := range entries {
		files[index] = entry.Filename
	}
	return files, true
}

// playlistChanged is where the art tiers are settled. mpv reports the
// playlist once at observe time and again on every change, and the bridge
// resolves each item's art before the display asks for any of it. mpv also
// reports the property when only the playing row moves, so the file names
// decide whether anything changed, and each file is opened once per
// playlist.
func (c *commander) playlistChanged(data json.RawMessage) {
	files, ok := parsePlaylistFiles(data)
	if !ok {
		return
	}
	c.artMutex.Lock()
	unchanged := slices.Equal(files, c.playlistFiles)
	c.artMutex.Unlock()
	if unchanged {
		return
	}

	tracks := make([]trackEntry, len(files))
	for index, file := range files {
		tracks[index] = resolveTrack(c.blockForItem(index+1), file)
	}

	c.artMutex.Lock()
	c.playlistFiles = files
	c.tracks = tracks
	pending := c.pendingAlbum
	c.pendingAlbum = nil
	c.artMutex.Unlock()

	// A request held from before the playlist was known is served here, now
	// that the item's art is settled, and it takes the ordinary path, so it
	// answers with the cover or with the empty path the same way any other
	// request does. It runs on a goroutine of its own, because this is the
	// reporter's goroutine, and a decode or a fetch must never hold up the
	// position reports.
	if pending != nil {
		go c.serveAlbum(*pending)
	}
}

// serveAlbum decodes the current item's cover to the box the display asks for,
// caches the blob by size, and replies. A size already decoded for the current
// item replies from the cache and decodes nothing.
//
// Every request the bridge can answer is answered, including the ones with
// nothing to show: an item with no art, and a decode that failed, both reply
// with an empty path. Silence would read as a slow decode, so the display
// would ask again on every redraw and never stop, and an empty path is the
// answer that ends the asking.
//
// The one exception: a request for an item the bridge has not resolved yet
// is held rather than answered, because the display asks once and keeps the
// answer, so an empty path sent before the playlist arrived would leave the
// cover dark for the whole run. playlistChanged serves the held request the
// moment it knows.
func (c *commander) serveAlbum(request artRequest) {
	kind, w, h := request.kind, request.width, request.height
	key := kind + ":" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
	c.artMutex.Lock()
	item := c.artItem
	if blob, cached := c.artCache[key]; cached {
		c.artMutex.Unlock()
		c.replyArt(kind, blob)
		return
	}
	track, known := c.trackFor(item)
	if !known {
		held := request
		c.pendingAlbum = &held
		c.artMutex.Unlock()
		return
	}
	c.artMutex.Unlock()

	if track.tier == artTierNone {
		c.replyArt(kind, artBlob{})
		return
	}

	blob, err := c.decodeAlbumArt(item, track, w, h)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: album art %q: %v\n", track.source, err)
		c.replyArt(kind, artBlob{})
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

// trackFor returns one item's resolved art, and says so when the playlist
// holds no such item. The caller holds artMutex.
func (c *commander) trackFor(item int) (trackEntry, bool) {
	index := item - 1
	if index < 0 || index >= len(c.tracks) {
		return trackEntry{}, false
	}
	return c.tracks[index], true
}

// decodeAlbumArt reads one track's art, scales it into the box, and writes the
// bgra to the shared volume.
func (c *commander) decodeAlbumArt(item int, track trackEntry, w, h int) (artBlob, error) {
	reader, err := openTrackArt(track)
	if err != nil {
		return artBlob{}, err
	}
	defer reader.Close()

	pixels, outW, outH, stride, err := scaleToBGRA(reader, w, h)
	if err != nil {
		return artBlob{}, err
	}

	path := filepath.Join(c.artDir, fmt.Sprintf("album-%d-%dx%d.bgra", item, w, h))
	if err := os.WriteFile(path, pixels, 0o644); err != nil {
		return artBlob{}, err
	}
	return artBlob{path: path, width: outW, height: outH, stride: stride}, nil
}
