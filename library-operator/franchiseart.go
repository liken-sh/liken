package main

// franchiseart.go downloads the art each franchise.yaml links to into the
// Library's own claim, under the same directory name the checkout uses. The
// repository holds links and no bytes, because a franchise is an opinion about
// a story and the art belongs to whoever published it. The files land under
// Kodi's names, the names plan 30's art facts write, so the rows read them
// through discoverArt the way a movie's are read. One ledger, .liken/art.yaml,
// records the link every file came from: the same link is never read twice,
// and a changed link is read again. A file with no entry in that ledger is the
// owner's, and the fetch never writes over it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// franchiseArtFact names the ledger this fetch writes, .liken/art.yaml. It is
// one file for one writer, the rule every .liken file follows, and the
// franchise art fetch is that one writer.
const franchiseArtFact = "art"

// The bounds on one download: the wait, and the bytes it may answer with. 20
// MiB is far above any poster and far below a file that would fill the claim.
// Both are variables so a test can drive a cap a small answer crosses.
var (
	franchiseArtTimeout       = 30 * time.Second
	franchiseArtSizeCap int64 = 20 << 20
)

// franchiseArtExtensions are the two image types the fetch writes, and the
// extension each takes. The extension comes from the answer and never from the
// URL, because a link carries no promise about what it serves.
var franchiseArtExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
}

// franchiseArtUserAgent names this fetch to the host it reads from. Wikimedia
// refuses a request that carries a generic client name, and answers one
// that says who is asking and where to read about it.
const franchiseArtUserAgent = "liken-library-operator (+https://library.liken.sh/)"

// franchiseArtSuffixes are the extensions the fetch reads back when it looks
// for a file it or the owner already wrote.
var franchiseArtSuffixes = []string{".jpg", ".jpeg", ".png"}

// franchiseArt is the art of one franchise, read off the claim the way a
// movie's is. A directory the fetch has not written yet holds no art, and that
// is an answer and not a failure, so a first scan still prunes.
func franchiseArt(artRoot, name string) (string, []string, error) {
	dir := filepath.Join(artRoot, name)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	return discoverArt(artRoot, dir)
}

// franchiseArtFetch is one scan Job's art fetch: the client it reads with, the
// write door it writes through, and the log it reports to. It holds no
// catalog, because the art is files on the claim and the rows are read from
// those files afterwards.
type franchiseArtFetch struct {
	client *http.Client
	writer *volumeWriter
	log    func(format string, args ...any)
}

// fetchAll downloads the art of every franchise the checkout holds, and
// returns how many files it wrote. A fetch that fails is logged and skipped,
// and the next scan asks again, because a link that is down for an hour must
// not fail a walk.
func (f franchiseArtFetch) fetchAll(ctx context.Context, checkout, artRoot string) int {
	names, err := franchiseDirectories(checkout)
	if err != nil {
		f.log("could not read the checkout for its art: %v", err)
		return 0
	}
	wrote := 0
	for _, name := range names {
		file, err := readFranchiseFile(checkout, name)
		if err != nil || file == nil {
			continue
		}
		wrote += f.fetchDirectory(ctx, filepath.Join(artRoot, name), name, file)
	}
	return wrote
}

// fetchDirectory downloads the art one franchise links to, in the order its
// keys are named. The ledger is read once for the directory, because each link
// writes a file of its own kind and no two of them meet.
func (f franchiseArtFetch) fetchDirectory(ctx context.Context, dir, name string,
	file *franchiseFile) int {
	links := file.artLinks()
	if len(links) == 0 {
		return 0
	}
	ledger, err := readLikenLedger(dir, franchiseArtFact)
	if err != nil {
		f.log("could not read the art ledger of %s: %v", name, err)
		return 0
	}
	wrote := 0
	for _, link := range links {
		if f.fetchOne(ctx, dir, name, link, ledger) {
			wrote++
		}
	}
	return wrote
}

// fetchOne handles one link: the file it names, whether this fetch wrote it,
// and whether the link has changed since. A file the ledger does not name is
// the owner's, and it stays. A file this fetch wrote from the same link is
// left alone, so a scan on a schedule reads nothing from the network.
func (f franchiseArtFetch) fetchOne(ctx context.Context, dir, name string,
	link franchiseArtLink, ledger likenLedger) bool {
	held := heldFranchiseArt(dir, link.Base)
	item, marked := ledger.itemAt(link.Base)
	if held != "" && !marked {
		return false
	}
	if held != "" && item.Source == link.URL {
		return false
	}

	data, kind, err := f.read(ctx, link.URL)
	if err != nil {
		f.log("could not read the %s of %s: %v", link.Base, name, err)
		return false
	}
	extension, known := franchiseArtExtensions[kind]
	if !known {
		f.log("the %s of %s answered %s, which is neither a jpeg nor a png",
			link.Base, name, kind)
		return false
	}

	target := link.Base + extension
	if err := f.writer.writeInto(dir, target, data); err != nil {
		f.log("could not write the %s of %s: %v", link.Base, name, err)
		return false
	}
	if held != "" && held != target {
		f.log("the %s of %s is now %s, and %s stands beside it", link.Base, name, target, held)
	}
	f.note(dir, name, link, target)
	return true
}

// note records which link this file came from, keyed by the kind of art. The
// mark and the file are two writes, so a mark that fails leaves a file the
// next scan reads as the owner's, and the log names it.
func (f franchiseArtFetch) note(dir, name string, link franchiseArtLink, target string) {
	err := f.writer.updateLikenLedger(dir, franchiseArtFact, func(ledger *likenLedger) {
		ledger.noteItem(likenItem{
			Path: link.Base, Source: link.URL, Written: time.Now().UTC(),
		})
	})
	if err != nil {
		f.log("wrote %s of %s and could not record the link it came from: %v", target, name, err)
	}
}

// heldFranchiseArt is the file one kind of art holds in a directory, whoever
// wrote it. The name is the kind and any image extension, because the owner's
// own poster may be a png where this fetch would write a jpg.
func heldFranchiseArt(dir, base string) string {
	for _, suffix := range franchiseArtSuffixes {
		if held, err := fileExists(filepath.Join(dir, base+suffix)); err == nil && held {
			return base + suffix
		}
	}
	return ""
}

// read is one download, bounded by the wait and by the size cap. The bytes are
// held in memory from the answer to the rename and no longer, which is what
// the cap bounds.
func (f franchiseArtFetch) read(ctx context.Context, url string) ([]byte, string, error) {
	bounded, cancel := context.WithTimeout(ctx, franchiseArtTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(bounded, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", franchiseArtUserAgent)
	response, err := f.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer drain(response.Body)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, "", fmt.Errorf("%s answered %s", url, response.Status)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, franchiseArtSizeCap+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > franchiseArtSizeCap {
		return nil, "", fmt.Errorf("%s answered more than the cap of %d bytes",
			url, franchiseArtSizeCap)
	}
	kind, _, _ := strings.Cut(response.Header.Get("Content-Type"), ";")
	return data, strings.TrimSpace(kind), nil
}
