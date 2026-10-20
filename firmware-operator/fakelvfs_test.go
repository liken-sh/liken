package main

// A fake LVFS behind an in-memory http.RoundTripper, so a test reads a
// catalog with no network and no socket, inside a synctest bubble. It
// serves each file at its URL, answers 304 to a read whose ETag is the
// file's own, and counts the reads of each URL. The builders write a
// catalog the way LVFS does: AppStream XML compressed with zstd, and a
// jcat of gzip JSON with bytes after the gzip stream.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// lvfsURL is the catalog's unversioned URL, as the lvfs Catalog names
// it.
const lvfsURL = "https://cdn.fwupd.org/downloads/firmware.xml.zst"

type fakeLVFS struct {
	mu       sync.Mutex
	files    map[string][]byte
	etags    map[string]string
	reads    map[string]int
	versions int
}

func newFakeLVFS() *fakeLVFS {
	return &fakeLVFS{files: map[string][]byte{}, etags: map[string]string{}, reads: map[string]int{}}
}

func (l *fakeLVFS) RoundTrip(r *http.Request) (*http.Response, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	address := r.URL.String()
	l.reads[address]++
	answer := func(status int, body []byte) *http.Response {
		header := http.Header{}
		if etag := l.etags[address]; etag != "" {
			header.Set("ETag", etag)
		}
		return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: header, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
	}
	body, held := l.files[address]
	switch {
	case !held:
		return answer(http.StatusNotFound, nil), nil
	case r.Header.Get("If-None-Match") != "" && r.Header.Get("If-None-Match") == l.etags[address]:
		return answer(http.StatusNotModified, nil), nil
	}
	return answer(http.StatusOK, body), nil
}

// read answers how many times a URL was read.
func (l *fakeLVFS) read(address string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reads[address]
}

// serve puts a file at a URL, with a new ETag.
func (l *fakeLVFS) serve(address string, body []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.versions++
	l.files[address] = body
	l.etags[address] = fmt.Sprintf(`"%d"`, l.versions)
}

// publish puts a catalog file in the catalog's directory, and a jcat
// that names it with its SHA-256, the way LVFS publishes a new catalog.
// It answers the file's name and SHA-256.
func (l *fakeLVFS) publish(file string, components ...fwComponent) (string, string) {
	body := zstdOf(appStreamOf(components...))
	l.serve(directoryOf(lvfsURL)+file, body)
	l.serve(lvfsURL+".jcat", jcatOf(file, sha256Hex(body)))
	return file, sha256Hex(body)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func directoryOf(address string) string {
	return address[:strings.LastIndex(address, "/")+1]
}

// fwComponent is one firmware component of a catalog: the GUIDs it
// flashes and its release versions.
type fwComponent struct {
	id       string
	guids    []string
	versions []string
}

// The GUIDs of the test fleet's devices.
const (
	biosGUID = "9c13b7f1-d618-5d68-be61-6b17881014a7"
	meGUID   = "69585d92-0000-0000-0000-000000000001"
	dbxGUID  = "23192307-0000-0000-0000-000000000002"
)

// frameworkBIOS is the component that offers the Framework's BIOS.
func frameworkBIOS(versions ...string) fwComponent {
	return fwComponent{id: "work.frame.Laptop.RyzenAI300.BIOS.firmware", guids: []string{biosGUID}, versions: versions}
}

// dbx is the component that offers the Secure Boot revocation list.
func dbx(versions ...string) fwComponent {
	return fwComponent{id: "org.uefi.dbx", guids: []string{dbxGUID}, versions: versions}
}

func appStreamOf(components ...fwComponent) string {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<components origin=\"lvfs\" version=\"0.9\">\n")
	for _, c := range components {
		fmt.Fprintf(&out, "  <component type=\"firmware\">\n    <id>%s</id>\n    <name>Firmware</name>\n    <provides>\n", c.id)
		for _, guid := range c.guids {
			fmt.Fprintf(&out, "      <firmware type=\"flashed\">%s</firmware>\n", guid)
		}
		out.WriteString("    </provides>\n    <requires><firmware compare=\"ge\" version=\"1\">00000000-0000-0000-0000-00000000ffff</firmware></requires>\n    <releases>\n")
		for _, version := range c.versions {
			fmt.Fprintf(&out, "      <release version=\"%s\" urgency=\"high\"><checksum type=\"sha256\">ab</checksum></release>\n", version)
		}
		out.WriteString("    </releases>\n  </component>\n")
	}
	out.WriteString("</components>\n")
	return out.String()
}

func zstdOf(text string) []byte {
	var out bytes.Buffer
	encoder, _ := zstd.NewWriter(&out)
	_, _ = encoder.Write([]byte(text))
	_ = encoder.Close()
	return out.Bytes()
}

// jcatOf builds a jcat with the blobs LVFS gives: a SHA-1, the SHA-256,
// and a signature. LVFS serves bytes after the end of the gzip stream,
// and so does this.
func jcatOf(file, sum string) []byte {
	document := fmt.Sprintf(`{"JcatVersionMajor":0,"JcatVersionMinor":1,"Items":[{"Id":%q,"Blobs":[`+
		`{"Kind":4,"Flags":1,"Timestamp":1,"Data":"0a0b0c0d0e0f00000000000000000000deadbeef"},`+
		`{"Kind":1,"Flags":1,"Timestamp":1,"Data":%q},`+
		`{"Kind":3,"Flags":1,"Timestamp":1,"Data":"-----BEGIN PKCS7-----"}]}]}`, file, sum)
	return gzipOf(document, []byte{0, 0, 0, 0})
}

func gzipOf(text string, trailing []byte) []byte {
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, _ = writer.Write([]byte(text))
	_ = writer.Close()
	return append(out.Bytes(), trailing...)
}
