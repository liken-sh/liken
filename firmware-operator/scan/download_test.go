package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSiblingURL(t *testing.T) {
	tests := []struct {
		name, catalog, file, want string
	}{
		{"a CDN path", testCatalogURL, testFile, testFileURL},
		{"a mirror with a query", "https://mirror.test/lvfs/firmware.xml.zst?x=1", "f.xml.zst", "https://mirror.test/lvfs/f.xml.zst?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := siblingURL(tt.catalog, tt.file)

			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestSiblingURLRejectsAnInvalidURL(t *testing.T) {
	if _, err := siblingURL("https://bad host/%zz", "f"); err == nil {
		t.Error("expected an error")
	}
}

func TestGetRefusesBodiesPastTheLimit(t *testing.T) {
	big := server{"https://x.test/big": make([]byte, maxDownload+1)}
	client := &http.Client{Transport: big}

	_, err := get(context.Background(), client, "https://x.test/big")

	if err == nil || !strings.Contains(err.Error(), "is larger than") {
		t.Errorf("got %v", err)
	}
}

func TestGetRejectsABadURL(t *testing.T) {
	if _, err := get(context.Background(), http.DefaultClient, "://"); err == nil {
		t.Error("expected an error")
	}
}
