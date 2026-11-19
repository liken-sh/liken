package main

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func gz(s string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(s))
	zw.Close()
	return buf.Bytes()
}

func TestParseJcat(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    catalogItem
		wantErr bool
	}{
		{"trailing garbage after the gzip stream", jcatFor(t, testFile, sum(testCatalog)), catalogItem{testFile, sum(testCatalog)}, false},
		{"no SHA-256 blob", gz(`{"Items":[{"Id":"f","Blobs":[{"Kind":4,"Data":"x"}]}]}`), catalogItem{}, true},
		{"no items", gz(`{"Items":[]}`), catalogItem{}, true},
		{"not JSON", gz(`<xml/>`), catalogItem{}, true},
		{"not gzip", []byte("plain"), catalogItem{}, true},
		{"a truncated stream", jcatFor(t, testFile, "abc")[:20], catalogItem{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJcat(tt.data)

			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseJcat = %+v, %v, want %+v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// The scan takes the checksum and limits the size of the jcat as the
// operator does (readJcat in the operator's jcat.go), so both programs
// compare the catalog with the same checksum and refuse the same files.
// Only the operator checks that the Id is a file name: loadCatalog
// reports CatalogMoved unless the Id equals the file that the operator
// checked, before it builds a URL from the Id.
func TestParseJcatTakesTheChecksumAndTheSizeLimitOfTheOperator(t *testing.T) {
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	tests := []struct {
		name    string
		data    []byte
		want    catalogItem
		wantErr bool
	}{
		{"two SHA-256 blobs", gz(`{"Items":[{"Id":"f","Blobs":[{"Kind":1,"Data":"` + first + `"},{"Kind":1,"Data":"` + second + `"}]}]}`),
			catalogItem{"f", first}, false},
		{"a SHA-256 blob that is not 64 hex digits", gz(`{"Items":[{"Id":"f","Blobs":[{"Kind":1,"Data":"not a digest"}]}]}`),
			catalogItem{}, true},
		{"a jcat that decompresses to more than 1 MiB", gz(`{"Items":[{"Id":"f","Blobs":[{"Kind":1,"Data":"` + first + `"}]}],"Pad":"` + strings.Repeat(" ", 1<<20) + `"}`),
			catalogItem{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJcat(tt.data)

			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseJcat = %+v, %v, want %+v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
