package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
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
// SHA-256, which is empty when the jcat has none.
type catalogItem struct {
	file   string
	sha256 string
}

// parseJcat reads the first item of a jcat file, which is gzip-compressed
// JSON. The LVFS server can append bytes after the gzip stream, so the
// reader stops at the end of the first gzip member. parseJcat does not
// check the signatures, because fwupdtool refresh checks them.
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
		return catalogItem{}, errors.New("the jcat has no item")
	}
	item := catalogItem{file: parsed.Items[0].ID}
	for _, blob := range parsed.Items[0].Blobs {
		if blob.Kind == jcatKindSHA256 {
			item.sha256 = blob.Data
		}
	}
	return item, nil
}
