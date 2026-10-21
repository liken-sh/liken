package main

import "testing"

func TestFreeBytesReadsTheFilesystem(t *testing.T) {
	free, err := freeBytes(t.TempDir())
	if err != nil || free <= 0 {
		t.Errorf("freeBytes = %d, %v, want a positive count", free, err)
	}
}
