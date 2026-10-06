package main

// The status writer composes every status once a window, and most of
// them match what the API server holds. The comparison with the stored
// status reads both through JSON (equalJSON), because a number read
// from a watch's store and the same number composed by the operator
// have different Go types. A device's status lists every property of
// its driver, so that comparison costs more than the rest of a window.
// The memo records the JSON of the status that each object holds at
// one resourceVersion, as the operator last wrote it or found it equal.
// While the object keeps that version, the writer compares the JSON of
// the composed status with the record alone: the same JSON is current,
// and other JSON is a change to write.

import "bytes"

type statusMemo struct {
	seen map[string]statusSeen
	// kept collects the records of the current window. Each window
	// starts it again, so the record of a deleted object goes.
	kept map[string]statusSeen
}

type statusSeen struct {
	version string
	body    []byte
}

// begin starts a window with the records of the window before.
func (m *statusMemo) begin() {
	m.seen, m.kept = m.kept, map[string]statusSeen{}
}

// knows reports whether the memo records the status of an object at a
// version. The record stays for the next window.
func (m *statusMemo) knows(key, version string) bool {
	s, ok := m.seen[key]
	if ok && s.version == version {
		m.kept[key] = s
	}
	return ok && s.version == version
}

// same reports whether an object holds a status at a version. A status
// that does not encode, such as one with a NaN reading, has no JSON and
// matches no record.
func (m *statusMemo) same(key, version string, body []byte) bool {
	s, ok := m.seen[key]
	return ok && body != nil && s.version == version && bytes.Equal(s.body, body)
}

// note records that an object holds a status at a version.
func (m *statusMemo) note(key, version string, body []byte) {
	if body != nil && version != "" {
		m.kept[key] = statusSeen{version, body}
	}
}
