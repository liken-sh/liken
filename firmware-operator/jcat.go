package main

// A jcat is the small file beside an LVFS catalog that names the
// catalog's current versioned file and its checksums and signatures.
// It is gzip-compressed JSON. Each item names one file in Id, and each
// blob holds one checksum or signature of it, with its kind.
//
// The operator reads only the name and the SHA-256. It does not check
// the signature, because it never decides that a release applies: it
// reads the catalog only to choose which machines to scan again. fwupd
// checks the signature in each scan pod, and refuses a catalog that
// fails.

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

// jcatSHA256 is the blob kind of a SHA-256 checksum.
const jcatSHA256 = 1

// jcatEntry is the file a jcat names and its SHA-256, in lowercase hex.
type jcatEntry struct {
	file   string
	sha256 string
}

// hexSHA256 matches a SHA-256 in hex.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// readJcat reads the first item of a jcat. LVFS serves the gzip stream
// with bytes after its end, so the reader stops at the end of the
// first member and ignores the rest.
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
		return jcatEntry{}, errors.New("the jcat names no file")
	}
	item := parsed.Items[0]
	// The name becomes part of a URL in the catalog's directory, so a
	// name that leaves the directory is refused.
	if item.ID == "" || strings.ContainsAny(item.ID, "/\\?#") || item.ID == "." || item.ID == ".." {
		return jcatEntry{}, fmt.Errorf("the jcat names the file %q, which is not a file name", item.ID)
	}
	for _, blob := range item.Blobs {
		sum := strings.ToLower(blob.Data)
		if blob.Kind == jcatSHA256 && hexSHA256.MatchString(sum) {
			return jcatEntry{file: item.ID, sha256: sum}, nil
		}
	}
	return jcatEntry{}, fmt.Errorf("the jcat gives no SHA-256 for %s", item.ID)
}

// maxJcatSize bounds a decompressed jcat. LVFS's jcat holds one item
// with a few checksums and signatures, so a larger one is a mistake.
const maxJcatSize = 1 << 20
