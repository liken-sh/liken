package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
)

// The blob kind of a SHA-256 digest in a jcat file.
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

// catalogItem is what the scan needs from a jcat: the versioned file
// name, and the SHA-256 the jcat gives for it, empty when it gives none.
type catalogItem struct {
	file   string
	sha256 string
}

// parseJcat reads the first item of a jcat file. A jcat is gzip-compressed
// JSON, and the LVFS server can append bytes after the gzip stream. The
// reader stops at the end of the first member, so those bytes never
// reach the decoder. fwupd checks the signatures in the same file when
// it refreshes, so this program reads only the name and the digest.
func parseJcat(data []byte) (catalogItem, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return catalogItem{}, err
	}
	zr.Multistream(false)
	plain, err := io.ReadAll(zr)
	if err != nil {
		return catalogItem{}, err
	}
	var parsed jcat
	if err := json.Unmarshal(plain, &parsed); err != nil {
		return catalogItem{}, err
	}
	if len(parsed.Items) == 0 {
		return catalogItem{}, errors.New("the jcat holds no item")
	}
	item := catalogItem{file: parsed.Items[0].ID}
	for _, blob := range parsed.Items[0].Blobs {
		if blob.Kind == jcatKindSHA256 {
			item.sha256 = blob.Data
		}
	}
	return item, nil
}
