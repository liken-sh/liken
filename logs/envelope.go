package main

// The envelope is the relay's whole output contract: one JSON object
// per line on stdout, a thin structured wrapper around a verbatim
// message. The rule for what gets lifted into fields is strict: only
// what the source format itself defines as a header (kmsg's priority
// and sequence, logrus's time= and level=). The message body is never
// parsed or rewritten; making sense of it is the job of whatever log
// stack someone chooses to run, not the OS's.
//
// Event time rides inside the envelope because the container runtime
// stamps log lines at the moment the relay wrote them, and a relay
// replaying a boot's worth of records writes them all at once. The
// runtime's timestamp answers "when was this relayed"; the envelope's
// answers "when did this happen".

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// envelope is one relayed log line. Field order here is field order
// in the output, since encoding/json emits struct fields in
// declaration order.
type envelope struct {
	// Time is the event time in RFC3339 with nanoseconds, UTC.
	Time string `json:"time"`

	// Severity is a syslog severity word (emerg through debug),
	// lifted from the source's header or defaulted to info.
	Severity string `json:"severity"`

	// Facility appears only on the kmsg relays, where it is the
	// syslog facility that separates the kernel's records (0) from
	// userspace's (1). It is a pointer so that the kernel's zero
	// serializes instead of vanishing behind omitempty.
	Facility *int `json:"facility,omitempty"`

	// Seq orders and deduplicates records within one source: the
	// kernel's own sequence number for kmsg, the line's starting
	// byte offset for tailed files. A consumer that sees the same
	// (source, seq) twice is seeing a replay, not a new event.
	Seq uint64 `json:"seq"`

	// Message is the original line, verbatim.
	Message string `json:"message"`
}

// envelopeWriter encodes envelopes one per line, each delivered to
// the underlying writer in a single Write call. That single call
// matters: the container runtime treats each write to the pod's
// stdout pipe as a unit, so a whole line per write is what keeps
// envelopes from interleaving in the pod's log file.
type envelopeWriter struct {
	w   io.Writer
	buf bytes.Buffer
	enc *json.Encoder
}

func newEnvelopeWriter(w io.Writer) *envelopeWriter {
	ew := &envelopeWriter{w: w}
	ew.enc = json.NewEncoder(&ew.buf)
	// Log lines are full of < and > (Go struct dumps, YAML snippets);
	// escaping them for HTML would garble the verbatim body.
	ew.enc.SetEscapeHTML(false)
	return ew
}

// emit writes one envelope as one line. Encode appends the newline.
func (ew *envelopeWriter) emit(e envelope) error {
	ew.buf.Reset()
	if err := ew.enc.Encode(e); err != nil {
		return err
	}
	_, err := ew.w.Write(ew.buf.Bytes())
	return err
}

// notice emits an envelope about the relay itself (a lost-records
// warning, a startup marker). Notices flow through the same contract
// as everything else so a consumer never needs a second parser; the
// liken-logs: prefix is what marks them as the relay speaking.
func (ew *envelopeWriter) notice(now time.Time, severity string, seq uint64, facility *int, message string) error {
	return ew.emit(envelope{
		Time:     now.UTC().Format(time.RFC3339Nano),
		Severity: severity,
		Facility: facility,
		Seq:      seq,
		Message:  "liken-logs: " + message,
	})
}
