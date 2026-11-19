package main

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

// jcatKindSHA256 is libjcat's blob kind for a SHA-256 digest.
const jcatKindSHA256 = 1

type jcat struct {
	Items []struct {
		ID    string `json:"Id"`
		Blobs []struct {
			Kind int    `json:"Kind"`
			Data string `json:"Data"`
		} `json:"Blobs"`
	} `json:"Items"`
}

// catalogItem is the versioned catalog file that a jcat lists, and its
// SHA-256.
type catalogItem struct {
	file   string
	sha256 string
}

// maxJcatSize limits how many decompressed bytes parseJcat reads, as
// the operator's readJcat limits them. A jcat from LVFS has one item
// with a few checksums and signatures, far less than this, and a small
// file that decompresses to more would fill the pod's memory.
const maxJcatSize = 1 << 20

// hexSHA256 is the form of a SHA-256 blob that the operator accepts.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseJcat reads the first item of a jcat file, which is gzip-compressed
// JSON. The LVFS server can append bytes after the gzip stream, so the
// reader stops at the end of the first gzip member. parseJcat does not
// check the signatures, because fwupdtool refresh checks them.
//
// It takes the checksum as the operator's readJcat does: the first
// SHA-256 blob that is 64 hex digits, in lowercase. So the scan compares
// the catalog with the checksum that the operator checked, and refuses a
// jcat that the operator refuses.
func parseJcat(data []byte) (catalogItem, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return catalogItem{}, err
	}
	zr.Multistream(false)
	plain, err := io.ReadAll(io.LimitReader(zr, maxJcatSize+1))
	if err != nil {
		return catalogItem{}, err
	}
	if len(plain) > maxJcatSize {
		return catalogItem{}, fmt.Errorf("the jcat decompresses to more than %d bytes", maxJcatSize)
	}
	var parsed jcat
	if err := json.Unmarshal(plain, &parsed); err != nil {
		return catalogItem{}, err
	}
	if len(parsed.Items) == 0 {
		return catalogItem{}, errors.New("the jcat has no item")
	}
	item := parsed.Items[0]
	for _, blob := range item.Blobs {
		sum := strings.ToLower(blob.Data)
		if blob.Kind == jcatKindSHA256 && hexSHA256.MatchString(sum) {
			return catalogItem{file: item.ID, sha256: sum}, nil
		}
	}
	return catalogItem{}, fmt.Errorf("the jcat has no SHA-256 blob for %s", item.ID)
}
