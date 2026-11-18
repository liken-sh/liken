package main

// The status writer composes every object's status once per window,
// and most of them equal the stored status. equalJSON compares the two
// by encoding and decoding both, because a number from the watch's cache
// and the same number composed by the operator have different Go types.
// A device's status lists every property of its driver, so this costs
// more than the rest of the window.
//
// statusMemo records the JSON of each status that the writer wrote or
// found equal, with the object's resourceVersion. While the object stays
// at that version, the writer compares the new JSON with the recorded
// JSON instead.

import "bytes"

type statusMemo struct {
	// previous holds the entries of the last window, which this window
	// reads.
	previous map[string]memoEntry
	// current collects the entries for the next window. An entry that
	// this window does not copy into current is dropped, such as the
	// entry of a deleted object.
	current map[string]memoEntry
	failed  bool
}

type memoEntry struct {
	version string
	body    []byte
}

// begin starts a window.
func (m *statusMemo) begin() {
	m.previous, m.current, m.failed = m.current, map[string]memoEntry{}, false
}

// knows reports whether the memo has an entry for an object at a
// version, and copies that entry into the next window.
func (m *statusMemo) knows(key, version string) bool {
	s, ok := m.previous[key]
	if ok && s.version == version {
		m.current[key] = s
	}
	return ok && s.version == version
}

// same reports whether the memo's entry for an object at a version has
// the given JSON. A status that does not encode, such as one with a NaN
// reading, has nil JSON and matches no entry, so it is written.
func (m *statusMemo) same(key, version string, body []byte) bool {
	s, ok := m.previous[key]
	return ok && body != nil && s.version == version && bytes.Equal(s.body, body)
}

// note records the JSON of an object's status at a version, unless the
// JSON or the version is empty.
func (m *statusMemo) note(key, version string, body []byte) {
	if body != nil && version != "" {
		m.current[key] = memoEntry{version, body}
	}
}
