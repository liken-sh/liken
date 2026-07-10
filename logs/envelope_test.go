package main

// The envelope's JSON is a contract read by strangers (whatever log
// stack someone runs), so these tests pin the exact bytes: field
// order, escaping, and when facility appears.

import (
	"bytes"
	"testing"
	"time"
)

func TestEnvelopeGoldenBytes(t *testing.T) {
	facility := 1
	cases := []struct {
		name string
		e    envelope
		want string
	}{
		{
			name: "kmsg record with facility",
			e: envelope{
				Time:     "2026-07-07T12:00:05Z",
				Severity: "info",
				Facility: &facility,
				Seq:      100,
				Message:  "liken: hello from userspace",
			},
			want: `{"time":"2026-07-07T12:00:05Z","severity":"info","facility":1,"seq":100,"message":"liken: hello from userspace"}` + "\n",
		},
		{
			name: "tailed line omits facility",
			e: envelope{
				Time:     "2026-07-07T12:00:05Z",
				Severity: "err",
				Seq:      4096,
				Message:  `time="2026-07-07T12:00:05Z" level=error msg="oops"`,
			},
			want: `{"time":"2026-07-07T12:00:05Z","severity":"err","seq":4096,"message":"time=\"2026-07-07T12:00:05Z\" level=error msg=\"oops\""}` + "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := newEnvelopeWriter(&buf).emit(c.e); err != nil {
				t.Fatal(err)
			}
			if buf.String() != c.want {
				t.Errorf("got  %q\nwant %q", buf.String(), c.want)
			}
		})
	}
}

// The kernel's facility is zero, which omitempty would swallow if
// Facility were a plain int; the pointer is what keeps it visible.
func TestEnvelopeKernelFacilityZeroSerializes(t *testing.T) {
	facility := 0
	var buf bytes.Buffer
	err := newEnvelopeWriter(&buf).emit(envelope{
		Time: "2026-07-07T12:00:05Z", Severity: "notice", Facility: &facility, Seq: 7, Message: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"time":"2026-07-07T12:00:05Z","severity":"notice","facility":0,"seq":7,"message":"x"}` + "\n"
	if buf.String() != want {
		t.Errorf("got  %q\nwant %q", buf.String(), want)
	}
}

// Log lines are full of angle brackets and quotes; the verbatim body
// must survive them (and never be HTML-escaped into < noise).
func TestEnvelopePreservesAwkwardBytes(t *testing.T) {
	var buf bytes.Buffer
	err := newEnvelopeWriter(&buf).emit(envelope{
		Time: "t", Severity: "info", Seq: 0, Message: `watch <nil> said "no" & left`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"time":"t","severity":"info","seq":0,"message":"watch <nil> said \"no\" & left"}` + "\n"
	if buf.String() != want {
		t.Errorf("got  %q\nwant %q", buf.String(), want)
	}
}

func TestNoticesAreEnvelopesWithAPrefix(t *testing.T) {
	var buf bytes.Buffer
	ew := newEnvelopeWriter(&buf)
	when := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	if err := ew.notice(when, "warning", 42, nil, "records were lost"); err != nil {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, buf.String())
	if len(es) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(es))
	}
	if es[0].Severity != "warning" || es[0].Seq != 42 {
		t.Errorf("got %+v", es[0])
	}
	if es[0].Message != "liken-logs: records were lost" {
		t.Errorf("message: %q", es[0].Message)
	}
}
