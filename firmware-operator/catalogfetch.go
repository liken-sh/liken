package main

// The operator reads a FirmwareCatalog's jcat when the FirmwareCatalog changes, once
// after the operator starts, when a scan answers CatalogMoved, and once
// an interval after the last read. LVFS publishes no feed of its
// changes to subscribe to, so the interval is the only way to see a new
// catalog. Each read sends the ETag of the last one, so an unchanged
// jcat costs one small answer.
//
// When the jcat names a new file, the operator downloads that file
// once, checks its SHA-256 against the jcat, and reads it into a
// catalogIndex. The index is a cache: the status holds the file's name
// and SHA-256, and an operator that needs the index after a restart
// downloads the file again and checks it against the status.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// discoverySettings are the operator's flags for discovery.
type discoverySettings struct {
	// scanImage is the image of the scan pods.
	scanImage string
	// interval is the time between two reads of a jcat.
	interval time.Duration
	// http reads the catalogs. Its timeout bounds each read, because a
	// read runs inside the pass.
	http *http.Client
}

// catalogCache is what the operator keeps of each FirmwareCatalog in memory:
// whether it has read the jcat since it started, whether a rehearsal or
// an apply found the jcat naming another file since the last read, and
// the index of the file. All three are caches. Losing them costs one
// read of the jcat and one download of the file. A lost mark of a moved
// jcat costs at most one interval, after which the timed read finds the
// new file.
type catalogCache struct {
	read  bool
	moved bool
	index *catalogIndex
}

// catalogMoved marks a FirmwareCatalog's jcat for a read at once. A
// scan that answers CatalogMoved reads it in the same pass, but a
// rehearsal or an apply ends later in the pass, after the catalogs are
// answered, so its mark waits for the next pass. The delete of the
// finished pod wakes that pass.
func (o *operator) catalogMoved(name string) {
	o.cache(name).moved = true
}

// fetched is what one read of a jcat gives a FirmwareCatalog's status.
type fetched struct {
	file, sha256, etag, checked string
	ready                       condition
}

// errChecksum marks a download whose SHA-256 differs from the jcat's.
var errChecksum = errors.New("the SHA-256 differs")

// maxFileSize bounds a downloaded catalog file. LVFS's compressed
// catalog is a few megabytes.
const maxFileSize = 64 << 20

// refreshDue reports whether a FirmwareCatalog's jcat needs a read, and when
// the next timed read is due.
func (o *operator) refreshDue(c *catalog, moved bool, now time.Time) (bool, time.Time) {
	next := parseTime(c.Status.Checked).Add(o.discovery.interval)
	r := c.Status.ready()
	cached := o.cache(c.Metadata.Name)
	due := moved || cached.moved || !cached.read || r.Status == "" || r.ObservedGeneration != c.Metadata.Generation || !now.Before(next)
	return due, next
}

// refresh reads a FirmwareCatalog's jcat, and the file it names when that file
// is new. It answers what the status takes. A failure keeps the last
// good file and its ETag, so the next read gets the whole jcat again.
func (o *operator) refresh(ctx context.Context, c *catalog, now time.Time) fetched {
	out := fetched{file: c.Status.File, sha256: c.Status.SHA256, etag: c.Status.ETag, checked: stamp(now)}
	cached := o.cache(c.Metadata.Name)
	cached.read, cached.moved = true, false
	fail := func(reason string, err error) fetched {
		out.ready = condition{Type: ready, Status: conditions.False, Reason: reason, Message: err.Error()}
		return out
	}
	etag := ""
	if c.Status.File != "" {
		etag = c.Status.ETag
	}
	body, nextTag, err := o.get(ctx, c.Spec.URL+".jcat", etag, maxJcatSize)
	if err != nil {
		return fail(reasonJcatUnreadable, err)
	}
	entry := jcatEntry{file: c.Status.File, sha256: c.Status.SHA256}
	if body != nil {
		if entry, err = readJcat(body); err != nil {
			return fail(reasonJcatUnreadable, fmt.Errorf("%s.jcat: %w", c.Spec.URL, err))
		}
	}
	if entry != (jcatEntry{file: c.Status.File, sha256: c.Status.SHA256}) {
		index, err := o.download(ctx, c.Spec.URL, entry)
		if errors.Is(err, errChecksum) {
			return fail(reasonChecksumMismatch, err)
		}
		if err != nil {
			return fail(reasonFileUnreadable, err)
		}
		cached.index = &index
	}
	out.file, out.sha256, out.etag = entry.file, entry.sha256, nextTag
	out.ready = condition{Type: ready, Status: conditions.True, Reason: reasonFetched, Message: "the jcat names " + entry.file}
	return out
}

// indexOf answers the index of a file that a FirmwareCatalog's status names,
// and downloads it when the cache does not hold it.
func (o *operator) indexOf(ctx context.Context, c *catalog, file, sum string) (catalogIndex, error) {
	cached := o.cache(c.Metadata.Name)
	if cached.index != nil && cached.index.file == file {
		return *cached.index, nil
	}
	index, err := o.download(ctx, c.Spec.URL, jcatEntry{file: file, sha256: sum})
	if err != nil {
		return catalogIndex{}, err
	}
	cached.index = &index
	return index, nil
}

func (o *operator) cache(name string) *catalogCache {
	cached, held := o.catalogCaches[name]
	if !held {
		cached = &catalogCache{}
		o.catalogCaches[name] = cached
	}
	return cached
}

// forgetCaches drops the caches of the FirmwareCatalogs that are gone, so the
// memory grows only with the FirmwareCatalogs.
func (o *operator) forgetCaches(present map[string]bool) {
	for name := range o.catalogCaches {
		if !present[name] {
			delete(o.catalogCaches, name)
		}
	}
}

// download reads the versioned file of a jcat entry from the directory
// of the catalog's URL, checks its SHA-256, and reads it.
func (o *operator) download(ctx context.Context, base string, entry jcatEntry) (catalogIndex, error) {
	address, err := fileURL(base, entry.file)
	if err != nil {
		return catalogIndex{}, err
	}
	body, _, err := o.get(ctx, address, "", maxFileSize)
	if err != nil {
		return catalogIndex{}, err
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != entry.sha256 {
		return catalogIndex{}, fmt.Errorf("%s: %w: the file has %s, and the jcat names %s", address, errChecksum, got, entry.sha256)
	}
	index, err := readCatalog(entry.file, bytes.NewReader(body))
	if err != nil {
		return catalogIndex{}, fmt.Errorf("%s: %w", address, err)
	}
	return index, nil
}

// fileURL answers the URL of a file in the directory of a catalog's
// URL.
func fileURL(base, file string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("spec.url: %w", err)
	}
	return parsed.ResolveReference(&url.URL{Path: file}).String(), nil
}

// get reads one URL, and answers its body and ETag. With an ETag, an
// answer of 304 means nothing changed, and answers no body.
func (o *operator) get(ctx context.Context, address, etag string, limit int64) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := o.discovery.http.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	switch {
	case etag != "" && response.StatusCode == http.StatusNotModified:
		return nil, etag, nil
	case response.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("%s: %s", address, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", address, err)
	}
	if int64(len(body)) > limit {
		return nil, "", fmt.Errorf("%s: the answer is larger than %d bytes", address, limit)
	}
	return body, response.Header.Get("ETag"), nil
}

// stamp writes a time the way the API server keeps it, to the second.
func stamp(now time.Time) string {
	return now.UTC().Truncate(time.Second).Format(time.RFC3339)
}
