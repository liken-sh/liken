package main

// The operator reads a FirmwareCatalog's jcat when the FirmwareCatalog
// changes, once after the operator starts, when a scan returns
// CatalogMoved, and once every interval. LVFS has no change feed to
// subscribe to, so only the interval finds a new catalog. Each read
// sends the ETag of the previous one, so an unchanged jcat costs one
// 304 response.
//
// When the jcat lists a new file, the operator downloads it, checks its
// SHA-256 against the jcat, and parses it into a catalogIndex. The
// index is only a cache. The status keeps the file's name and SHA-256,
// so after a restart the operator downloads the file again and checks
// it against the status.

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

// discoverySettings are the operator's flags for reading catalogs.
type discoverySettings struct {
	scanImage string
	// interval is the time between timed reads of a jcat.
	interval time.Duration
	// http reads the catalogs. Its timeout bounds each read, because
	// reads run inside the reconcile pass.
	http *http.Client
}

// catalogCache is the in-memory state for one FirmwareCatalog. A restart
// loses it, which costs one jcat read and one file download. A lost
// moved flag delays the read of a new file until the next timed read.
type catalogCache struct {
	read  bool
	moved bool
	index *catalogIndex
}

// catalogMoved records that a rehearsal or an apply returned
// CatalogMoved, so the next pass reads the FirmwareCatalog's jcat. A
// pass judges rehearsals and applies after it reads the catalogs, so the
// read waits for the pass that the deletion of the finished pod starts.
// A scan's CatalogMoved does not come here: reconcileCatalog reads it
// before the read and passes it to refreshDue.
func (o *operator) catalogMoved(name string) {
	o.cache(name).moved = true
}

// fetched is the result of one jcat read, to be written to the
// FirmwareCatalog's status.
type fetched struct {
	file, sha256, etag, checked string
	ready                       condition
}

// errChecksum marks a download whose SHA-256 differs from the jcat's.
var errChecksum = errors.New("the SHA-256 differs")

const maxFileSize = 64 << 20

// refreshDue reports whether a FirmwareCatalog's jcat needs a read now,
// and when the next timed read is due.
func (o *operator) refreshDue(c *catalog, moved bool, now time.Time) (bool, time.Time) {
	next := parseTime(c.Status.Checked).Add(o.discovery.interval)
	r := c.Status.ready()
	cached := o.cache(c.Metadata.Name)
	due := moved || cached.moved || !cached.read || r.Status == "" || r.ObservedGeneration != c.Metadata.Generation || !now.Before(next)
	return due, next
}

// refresh reads a FirmwareCatalog's jcat, and downloads the file it
// lists when that file is new. On failure it returns a False Ready
// condition with the previous file, SHA-256, and ETag. The ETag must
// stay with the file it was read with: if the status kept the new ETag
// beside the old file, the next read would get 304 and never retry the
// download.
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
		return fail(reasonJcatUnreadable, fmt.Errorf("the operator cannot read the jcat: %w", err))
	}
	entry := jcatEntry{file: c.Status.File, sha256: c.Status.SHA256}
	if body != nil {
		if entry, err = readJcat(body); err != nil {
			return fail(reasonJcatUnreadable, fmt.Errorf("the operator cannot read the jcat: %s.jcat: %w", c.Spec.URL, err))
		}
	}
	if entry != (jcatEntry{file: c.Status.File, sha256: c.Status.SHA256}) {
		index, err := o.download(ctx, c.Spec.URL, entry)
		if errors.Is(err, errChecksum) {
			return fail(reasonChecksumMismatch, fmt.Errorf("the catalog file does not match the jcat: %w", err))
		}
		if err != nil {
			return fail(reasonFileUnreadable, fmt.Errorf("the operator cannot read the catalog file: %w", err))
		}
		cached.index = &index
	}
	out.file, out.sha256, out.etag = entry.file, entry.sha256, nextTag
	out.ready = condition{Type: ready, Status: conditions.True, Reason: reasonFetched, Message: "The jcat lists the catalog file " + entry.file}
	return out
}

// indexOf returns the index of the file that a FirmwareCatalog's status
// records. It downloads the file when the cache does not have it.
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
	cached, found := o.catalogCaches[name]
	if !found {
		cached = &catalogCache{}
		o.catalogCaches[name] = cached
	}
	return cached
}

// deleteCachesOfDeletedCatalogs deletes the caches of FirmwareCatalogs
// that no longer exist.
func (o *operator) deleteCachesOfDeletedCatalogs(present map[string]bool) {
	for name := range o.catalogCaches {
		if !present[name] {
			delete(o.catalogCaches, name)
		}
	}
}

// download fetches the file that a jcat entry names from the directory
// of the catalog's URL, checks its SHA-256 against the entry, and
// parses it.
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
		return catalogIndex{}, fmt.Errorf("%s: %w: the file has %s, and the jcat lists %s", address, errChecksum, got, entry.sha256)
	}
	index, err := readCatalog(entry.file, bytes.NewReader(body))
	if err != nil {
		return catalogIndex{}, fmt.Errorf("%s: %w", address, err)
	}
	return index, nil
}

// fileURL returns the URL of a file in the same directory as the
// catalog's URL.
func fileURL(base, file string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("spec.url: %w", err)
	}
	return parsed.ResolveReference(&url.URL{Path: file}).String(), nil
}

// get fetches one URL and returns its body and ETag. When etag is set
// and the server returns 304, get returns a nil body and the same
// ETag.
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
		return nil, "", fmt.Errorf("%s: the response is larger than %d bytes", address, limit)
	}
	return body, response.Header.Get("ETag"), nil
}

// stamp formats a time in UTC, truncated to the second, as the API
// server stores timestamps.
func stamp(now time.Time) string {
	return now.UTC().Truncate(time.Second).Format(time.RFC3339)
}
