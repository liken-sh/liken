package main

import (
	"strings"
	"testing"
)

func TestASumsFileListsTheDigestOfEachManifestEntry(t *testing.T) {
	entries := map[string]map[string]string{
		"reader": {"url": "https://example.test/upstream/ch_rec_infer.onnx", "sha256": "aaaa"},
	}

	findings := sumsFindings("models/models.sha256", "models/pin.toml", "bbbb  reader.onnx\n", entries)

	if len(findings) != 1 || !strings.Contains(findings[0], "reader") || !strings.Contains(findings[0], "aaaa") {
		t.Fatalf("the findings are %q, want one that names the entry reader and its sha256 aaaa", findings)
	}
}

func TestASumsFileMayRenameAManifestEntrysFile(t *testing.T) {
	entries := map[string]map[string]string{
		"reader": {"url": "https://example.test/upstream/ch_rec_infer.onnx", "sha256": "aaaa"},
	}

	findings := sumsFindings("models/models.sha256", "models/pin.toml", "aaaa  reader.onnx\ncccc  NOTICE\n", entries)

	if len(findings) != 0 {
		t.Fatalf("the findings are %q, want none for a file that the sums file lists under its own name", findings)
	}
}
