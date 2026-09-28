package main

// imdbcache.go reads one IMDb dataset file for the nfo container, through the
// cache on the provider's claim where the container mounts one. The cache
// holds each file in the gzipped form IMDb served, one directory per file:
//
//	title.ratings/
//	  3f2a9c1d0b7e6a54.tsv.gz   the copy, named by a hash of its ETag
//	  current.json              the ETag, Last-Modified, size, and download time
//
// Each run asks IMDb whether its copy is current with If-None-Match. A 304
// reads the copy from disk. A 200 streams the new file into the reader and
// into a temporary beside the copy in one pass, and the temporary replaces
// the copy only when the download is complete.

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// The record beside each copy.
type datasetRecord struct {
	ETag         string    `json:"etag"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
	Downloaded   time.Time `json:"downloaded"`
}

const datasetRecordName = "current.json"

// The name of the copy of one ETag. The ETag is quoted text, so a hash of it
// is a name a filesystem takes.
func datasetCopyName(etag string) string {
	sum := sha256.Sum256([]byte(etag))
	return hex.EncodeToString(sum[:8]) + ".tsv.gz"
}

var datasetCopyPattern = regexp.MustCompile(`^[0-9a-f]{16}\.tsv\.gz$`)

func isDatasetCopyName(name string) bool { return datasetCopyPattern.MatchString(name) }

// How long a container waits for another container on its node to finish
// the download of the same file. The largest file downloads in minutes on a
// slow link.
const datasetLockTimeout = 30 * time.Minute

// The reader of the dataset files: IMDb's address, the cache directory or
// none, and the writer the cache's renames go through.
type datasetFetcher struct {
	base   string
	cache  string
	client *http.Client
	writer *volumeWriter
	logf   func(string, ...any)
	// The pace between two file requests of this container, and when the last
	// one went out.
	pace time.Duration
	last time.Time
}

// read hands every row of one file to keep, and returns the Last-Modified
// time of the copy it read. With a cache, it reads through the cache. A cache
// that fails in any way, full, read-only, or not mounted, is logged with the
// filesystem's own text, and the file is read from IMDb with nothing written.
func (f *datasetFetcher) read(ctx context.Context, name string, keep func([][]byte)) (time.Time, error) {
	if f.cache == "" {
		return f.stream(ctx, name, keep)
	}
	dir := filepath.Join(f.cache, name)
	unlock, err := lockDatasetDirectory(ctx, dir)
	if err != nil {
		f.logf("could not use the cache of %s, so this run reads it from IMDb: %v", name, err)
		return f.stream(ctx, name, keep)
	}
	defer unlock()
	return f.readCached(ctx, dir, name, keep)
}

// The read under the lock. A second container on the same node takes the
// lock after the first, reads the record the first one wrote, and gets a 304.
func (f *datasetFetcher) readCached(ctx context.Context, dir, name string, keep func([][]byte)) (time.Time, error) {
	record, held := readDatasetRecord(dir)
	etag := ""
	if held {
		etag = record.ETag
	}
	response, err := f.get(ctx, name, etag)
	if err != nil {
		return time.Time{}, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotModified:
		f.logf("IMDb answered 304 for %s, so this run reads the cached copy", name)
		return record.LastModified, f.readCopy(filepath.Join(dir, datasetCopyName(record.ETag)), keep)
	case http.StatusOK:
		return f.download(dir, name, record, response, keep)
	}
	return time.Time{}, fmt.Errorf("IMDb answered %d for %s", response.StatusCode, name)
}

// The copy on disk, read through the gzip reader.
func (f *datasetFetcher) readCopy(path string, keep func([][]byte)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return readGzippedRows(file, keep)
}

// A read with no cache: the response body goes straight into the gzip
// reader, and nothing reaches the disk.
func (f *datasetFetcher) stream(ctx context.Context, name string, keep func([][]byte)) (time.Time, error) {
	response, err := f.get(ctx, name, "")
	if err != nil {
		return time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("IMDb answered %d for %s", response.StatusCode, name)
	}
	return lastModifiedOf(response), readGzippedRows(response.Body, keep)
}

// One GET, with If-None-Match where the cache holds a copy, no sooner than
// the pace after the last one.
func (f *datasetFetcher) get(ctx context.Context, name, etag string) (*http.Response, error) {
	if wait := f.pace - time.Since(f.last); !f.last.IsZero() && wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	f.last = time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, datasetURL(f.base, name), nil)
	if err != nil {
		return nil, err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	return f.client.Do(request)
}

func lastModifiedOf(response *http.Response) time.Time {
	modified, err := http.ParseTime(response.Header.Get("Last-Modified"))
	if err != nil {
		return time.Time{}
	}
	return modified.UTC()
}

// The download of a new version. The body is read once: into the gzip
// reader, and through the tee into a temporary beside the copy. The
// temporary replaces the copy only when the reader reached the end of the
// gzip stream, which checks its CRC, and the byte count equals
// Content-Length. A write to the temporary that fails ends the writes and
// not the read, so the rows still reach the fact.
func (f *datasetFetcher) download(dir, name string, previous datasetRecord, response *http.Response,
	keep func([][]byte)) (time.Time, error) {
	modified := lastModifiedOf(response)
	record := datasetRecord{ETag: response.Header.Get("ETag"), LastModified: modified}
	target := filepath.Join(dir, datasetCopyName(record.ETag))
	temporary := f.writer.temporary(target)
	staged := openDatasetTemporary(temporary)
	counted := &countingReader{reader: io.TeeReader(response.Body, staged)}
	if err := readGzippedRows(counted, keep); err != nil {
		staged.discard(f.writer)
		return time.Time{}, err
	}
	if response.ContentLength >= 0 && counted.count != response.ContentLength {
		staged.discard(f.writer)
		return time.Time{}, fmt.Errorf("IMDb sent %d of the %d bytes of %s",
			counted.count, response.ContentLength, name)
	}
	record.Size, record.Downloaded = counted.count, time.Now().UTC()
	if err := f.landCopy(dir, target, temporary, staged, previous, record); err != nil {
		f.logf("could not keep %s in the cache: %v", name, err)
	}
	return modified, nil
}

// The copy, then the record, then the delete of the copy before it. A
// container that has the previous copy open still reads it to the end,
// because a deleted file stays readable until its last handle closes.
func (f *datasetFetcher) landCopy(dir, target, temporary string, staged *datasetTemporary,
	previous datasetRecord, record datasetRecord) error {
	if err := staged.close(); err != nil {
		staged.discard(f.writer)
		return err
	}
	if err := f.writer.land(temporary, target); err != nil {
		staged.discard(f.writer)
		return err
	}
	// A record of strings, times, and a number always marshals.
	data, _ := json.Marshal(record)
	if err := f.writer.write(filepath.Join(dir, datasetRecordName), data); err != nil {
		return err
	}
	if previous.ETag != "" && datasetCopyName(previous.ETag) != filepath.Base(target) {
		err := f.writer.removeDatasetCopy(filepath.Join(dir, datasetCopyName(previous.ETag)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// The record of the copy a directory holds, and false where it holds none
// or the copy it names is gone.
func readDatasetRecord(dir string) (datasetRecord, bool) {
	data, err := os.ReadFile(filepath.Join(dir, datasetRecordName))
	if err != nil {
		return datasetRecord{}, false
	}
	var record datasetRecord
	if json.Unmarshal(data, &record) != nil || record.ETag == "" {
		return datasetRecord{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, datasetCopyName(record.ETag))); err != nil {
		return datasetRecord{}, false
	}
	return record, true
}

// Every row of one gzipped file. The gzip reader checks the CRC and the size
// of each member when it reaches the member's end, so a read that returns no
// error read a complete file.
func readGzippedRows(r io.Reader, keep func([][]byte)) error {
	unzipped, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer unzipped.Close()
	return readDatasetRows(unzipped, keep)
}

// The exclusive lock of one file's directory, which is one download at a time
// for each file on each node. The directory is made under the cache root, and
// a root that does not exist is an error, because it is a claim that is not
// mounted. The wait polls, so it ends with the context or the timeout.
func lockDatasetDirectory(ctx context.Context, dir string) (func(), error) {
	if _, err := os.Stat(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, volumeDirectoryPerm); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	handle, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(datasetLockTimeout)
	for {
		err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
				handle.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			handle.Close()
			return nil, fmt.Errorf("locking %s: %w", dir, err)
		}
		select {
		case <-ctx.Done():
			handle.Close()
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// The temporary a download fills. The first write that fails closes it and
// keeps the error, and every later write is dropped, so a full claim ends the
// copy and never the read.
type datasetTemporary struct {
	file *os.File
	err  error
}

func openDatasetTemporary(path string) *datasetTemporary {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, volumeFilePerm)
	return &datasetTemporary{file: file, err: err}
}

func (t *datasetTemporary) Write(data []byte) (int, error) {
	if t.err != nil {
		return len(data), nil
	}
	if _, err := t.file.Write(data); err != nil {
		t.err = err
	}
	return len(data), nil
}

// The flush and close that make the copy whole, or the error the writes met.
func (t *datasetTemporary) close() error {
	if t.err != nil {
		return t.err
	}
	if err := t.file.Sync(); err != nil {
		t.file.Close()
		return err
	}
	return t.file.Close()
}

// A copy that will not land is removed through the write door.
func (t *datasetTemporary) discard(writer *volumeWriter) {
	if t.file == nil {
		return
	}
	t.file.Close()
	_ = writer.removeTemporary(t.file.Name())
}

// The count of bytes that passed, which the download checks against
// Content-Length.
type countingReader struct {
	reader io.Reader
	count  int64
}

func (c *countingReader) Read(data []byte) (int, error) {
	read, err := c.reader.Read(data)
	c.count += int64(read)
	return read, err
}
