package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetFromOptions(t *testing.T) {
	cases := []struct {
		name     string
		opts     captureOptions
		resource string
		wantErr  bool
	}{
		{"a sink by default", captureOptions{Name: "room", Format: "wav"}, "sinks", false},
		{"a source when asked", captureOptions{Name: "mic", Source: true, Format: "wav"}, "sources", false},
		{"no name is an error", captureOptions{Format: "wav"}, "", true},
		{"an unknown format is an error", captureOptions{Name: "room", Format: "mp3"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, err := targetFromOptions(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("targetFromOptions(%+v) returned no error", tc.opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("targetFromOptions(%+v): %v", tc.opts, err)
			}
			if target.Resource != tc.resource {
				t.Fatalf("resource = %q, want %q", target.Resource, tc.resource)
			}
		})
	}
}

func TestCaptureRoute(t *testing.T) {
	cases := []struct {
		format string
		path   string
		accept string
	}{
		{"wav", "/v1/audio/sinks/room/audio.wav", "audio/wav"},
		{"flac", "/v1/audio/sinks/room/audio.flac", "audio/flac"},
		{"opus", "/v1/audio/sinks/room/audio.opus", "audio/ogg"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			path, accept, err := captureRoute(captureTarget{Resource: "sinks", Name: "room", Format: tc.format})
			if err != nil {
				t.Fatalf("captureRoute: %v", err)
			}
			if path != tc.path || accept != tc.accept {
				t.Fatalf("captureRoute = (%q, %q), want (%q, %q)", path, accept, tc.path, tc.accept)
			}
		})
	}
}

func TestCaptureRouteRejectsUnknownFormat(t *testing.T) {
	if _, _, err := captureRoute(captureTarget{Resource: "sinks", Name: "room", Format: "mp3"}); err == nil {
		t.Fatal("captureRoute returned no error for an unknown format")
	}
}

func TestStreamCaptureWritesOnlyMediaBytes(t *testing.T) {
	const media = "RIFFmedia-bytes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/sinks/room/audio.wav" {
			t.Errorf("the request's path is %q, want /v1/audio/sinks/room/audio.wav", r.URL.Path)
		}
		if r.Header.Get("Accept") != "audio/wav" {
			t.Errorf("Accept = %q, want audio/wav", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", r.Header.Get("Authorization"))
		}
		io.WriteString(w, media)
	}))
	defer server.Close()

	var out strings.Builder
	err := streamCapture(context.Background(), server.Client(), server.URL, "secret",
		captureTarget{Resource: "sinks", Name: "room", Format: "wav"}, &out)
	if err != nil {
		t.Fatalf("streamCapture: %v", err)
	}
	if out.String() != media {
		t.Fatalf("out = %q, want %q", out.String(), media)
	}
}

func TestStreamCaptureCarriesTheServerWords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "you may not tap this sink")
	}))
	defer server.Close()

	var out strings.Builder
	err := streamCapture(context.Background(), server.Client(), server.URL, "",
		captureTarget{Resource: "sinks", Name: "room", Format: "wav"}, &out)
	if err == nil {
		t.Fatal("streamCapture returned no error for a 403")
	}
	if !strings.Contains(err.Error(), "you may not tap this sink") {
		t.Fatalf("the error is %q, want one that includes the API's text", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out = %q after an error, want nothing", out.String())
	}
}

func TestAnUnknownFormatListsTheFormats(t *testing.T) {
	_, err := targetFromOptions(captureOptions{Name: "room", Format: "mp3"})
	if err == nil {
		t.Fatal("targetFromOptions returned no error for the format mp3")
	}
	for _, want := range []string{"mp3", "wav", "flac", "opus"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want one that names %q", err, want)
		}
	}
}

func TestRunCaptureRejectsBadOptionsBeforeTheCluster(t *testing.T) {
	err := runCapture(context.Background(), nil,
		captureOptions{Name: "room", Format: "mp3"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture reached the cluster with an unknown format")
	}
}

func TestCaptureClientNamesTheServiceDNS(t *testing.T) {
	client := captureClient(nil, nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the transport is %T, want *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig.ServerName != apiServiceDNS {
		t.Fatalf("ServerName = %q, want %q", transport.TLSClientConfig.ServerName, apiServiceDNS)
	}
}
