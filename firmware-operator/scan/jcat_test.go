package main

import (
	"bytes"
	"compress/gzip"
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
		{"trailing garbage after the gzip stream", jcatFor(t, testFile, "abc"), catalogItem{testFile, "abc"}, false},
		{"no SHA-256 blob", gz(`{"Items":[{"Id":"f","Blobs":[{"Kind":4,"Data":"x"}]}]}`), catalogItem{file: "f"}, false},
		{"no items", gz(`{"Items":[]}`), catalogItem{}, true},
		{"not JSON", gz(`<xml/>`), catalogItem{}, true},
		{"not gzip", []byte("plain"), catalogItem{}, true},
		{"a truncated stream", jcatFor(t, testFile, "abc")[:20], catalogItem{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJcat(tt.data)

			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("got %+v, %v; want %+v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
