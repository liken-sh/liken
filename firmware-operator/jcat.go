package main

// A jcat is the gzip-compressed JSON file that LVFS serves beside a
// catalog. Its first item has the name of the catalog's current
// versioned file in Id, and a list of blobs, each one a checksum or a
// signature of that file.
//
// The operator reads only the file name and its SHA-256, to detect a new
// catalog file and choose which machines to scan again. It does not
// check the signatures, because fwupd in each scan pod refuses a catalog
// whose jcat it cannot verify.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// jcatSHA256 is the value of Kind for a SHA-256 checksum blob.
const jcatSHA256 = 1

// jcatEntry is the catalog file name and its SHA-256, in lowercase hex,
// from a jcat.
type jcatEntry struct {
	file   string
	sha256 string
}

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// readJcat reads the first item of a jcat. LVFS can send bytes after
// the end of the gzip stream, so readJcat stops at the end of the first
// gzip member and ignores the rest.
func readJcat(body []byte) (jcatEntry, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return jcatEntry{}, fmt.Errorf("the jcat is not gzip: %w", err)
	}
	unzipped.Multistream(false)
	text, err := io.ReadAll(io.LimitReader(unzipped, maxJcatSize))
	if err != nil {
		return jcatEntry{}, fmt.Errorf("the jcat does not decompress: %w", err)
	}
	var parsed struct {
		Items []struct {
			ID    string `json:"Id"`
			Blobs []struct {
				Kind int    `json:"Kind"`
				Data string `json:"Data"`
			} `json:"Blobs"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(text, &parsed); err != nil {
		return jcatEntry{}, fmt.Errorf("the jcat is not JSON: %w", err)
	}
	if len(parsed.Items) == 0 {
		return jcatEntry{}, errors.New("the jcat has no items")
	}
	item := parsed.Items[0]
	// The caller builds a URL in the catalog's directory from this name,
	// so readJcat refuses a name that could point outside the directory.
	if item.ID == "" || strings.ContainsAny(item.ID, "/\\?#") || item.ID == "." || item.ID == ".." {
		return jcatEntry{}, fmt.Errorf("the first jcat item has the Id %q, which is not a file name", item.ID)
	}
	for _, blob := range item.Blobs {
		sum := strings.ToLower(blob.Data)
		if blob.Kind == jcatSHA256 && hexSHA256.MatchString(sum) {
			return jcatEntry{file: item.ID, sha256: sum}, nil
		}
	}
	return jcatEntry{}, fmt.Errorf("the jcat has no SHA-256 blob for %s", item.ID)
}

// maxJcatSize limits how many decompressed bytes readJcat reads. A jcat
// from LVFS has one item with a few checksums and signatures, far less
// than this.
const maxJcatSize = 1 << 20
