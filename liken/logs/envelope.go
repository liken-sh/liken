package main

// The envelope is the relay's entire output contract: one JSON object
// per line on stdout, a thin structured wrapper around a verbatim
// message. The rule for what moves into fields is strict: only what
// the source format itself defines as a header (kmsg's priority and
// sequence, logrus's time= and level=). The relay never parses or
// rewrites the message body. Making sense of the body is the job of
// whatever log stack someone chooses to run, not the job of the OS.
//
// Event time travels inside the envelope, because the container
// runtime stamps log lines at the moment the relay wrote them, and a
// relay that replays a boot's worth of records writes them all at
// once. The runtime's timestamp answers "when was this relayed". The
// envelope's time field answers "when did this happen".

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// envelope is one relayed log line. The field order here sets the
// field order in the output, because encoding/json writes struct
// fields in declaration order.
type envelope struct {
	// Time is the event time in RFC3339 with nanoseconds, UTC.
	Time string `json:"time"`

	// Severity is a syslog severity word (emerg through debug). It
	// comes from the source's header, or defaults to info.
	Severity string `json:"severity"`

	// Facility appears only on the kmsg relays. There, it is the
	// syslog facility that separates the kernel's records (0) from
	// userspace's records (1). It is a pointer so that the kernel's
	// zero value serializes, instead of disappearing behind
	// omitempty.
	Facility *int `json:"facility,omitempty"`

	// Seq orders and deduplicates records within one source. For
	// kmsg, this is the kernel's own sequence number. For tailed
	// files, this is the line's starting byte offset. A consumer
	// that sees the same (source, seq) twice is seeing a replay, not
	// a new event.
	Seq uint64 `json:"seq"`

	// Message is the original line, verbatim.
	Message string `json:"message"`
}

// envelopeWriter encodes envelopes one per line, and delivers each
// line to the underlying writer in a single Write call. That single
// call matters: the container runtime treats each write to the pod's
// stdout pipe as one unit. Writing a whole line per call is what
// keeps envelopes from interleaving in the pod's log file.
type envelopeWriter struct {
	w   io.Writer
	buf bytes.Buffer
	enc *json.Encoder
}

func newEnvelopeWriter(w io.Writer) *envelopeWriter {
	ew := &envelopeWriter{w: w}
	ew.enc = json.NewEncoder(&ew.buf)
	// Log lines are full of < and > characters, from Go struct dumps
	// and YAML snippets. Escaping them for HTML would corrupt the
	// verbatim body.
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

// notice emits an envelope about the relay itself, such as a
// lost-records warning or a startup marker. Notices follow the same
// contract as every other envelope, so a consumer never needs a
// second parser. The liken-logs: prefix marks these envelopes as
// coming from the relay, not from the source it reads.
func (ew *envelopeWriter) notice(now time.Time, severity string, seq uint64, facility *int, message string) error {
	return ew.emit(envelope{
		Time:     now.UTC().Format(time.RFC3339Nano),
		Severity: severity,
		Facility: facility,
		Seq:      seq,
		Message:  "liken-logs: " + message,
	})
}
