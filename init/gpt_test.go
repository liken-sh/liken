package main

// Tests for the GPT layout arithmetic. These pin the constants the
// partition planner in storage.go builds on: where a partition may
// start, and where the backup table forces the last one to end.

import "testing"

func TestGPTLastUsableLBA(t *testing.T) {
	// A 2 GiB disk is 4,194,304 sectors of 512 bytes. The table
	// reserves 34 sectors at each end (MBR + header + 32 entry
	// sectors in front; the mirror at the tail), so the last sector a
	// partition may occupy is 35 from the end.
	if got := gptLastUsableLBA(4_194_304); got != 4_194_269 {
		t.Errorf("gptLastUsableLBA(4194304) = %d, want 4194269", got)
	}
}

func TestAlignLBA(t *testing.T) {
	cases := []struct{ in, want uint64 }{
		{0, 0},         // already on a boundary
		{1, 2_048},     // anything inside the first MiB rounds up past the table
		{34, 2_048},    // the first sector after the primary table, likewise
		{2_048, 2_048}, // a boundary stays put
		{2_049, 4_096}, // one past a boundary rounds to the next
	}
	for _, c := range cases {
		if got := alignLBA(c.in); got != c.want {
			t.Errorf("alignLBA(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
