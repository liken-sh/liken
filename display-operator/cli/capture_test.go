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
		name    string
		opts    captureOptions
		format  string
		wantErr bool
	}{
		{"a clip by default", captureOptions{Name: "HDMI-A-1", Format: "mp4"}, "mp4", false},
		{"a frame when asked", captureOptions{Name: "HDMI-A-1", Format: "png"}, "png", false},
		{"no name is an error", captureOptions{Format: "mp4"}, "", true},
		{"an unknown format is an error", captureOptions{Name: "HDMI-A-1", Format: "gif"}, "", true},
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
			if target.Format != tc.format {
				t.Fatalf("format = %q, want %q", target.Format, tc.format)
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
		{"mp4", "/v1/display/displays/HDMI-A-1/screen.mp4", "video/mp4"},
		{"png", "/v1/display/displays/HDMI-A-1/screen.png", "image/png"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			path, accept, err := captureRoute(captureTarget{Name: "HDMI-A-1", Format: tc.format})
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
	if _, _, err := captureRoute(captureTarget{Name: "HDMI-A-1", Format: "gif"}); err == nil {
		t.Fatal("captureRoute returned no error for an unknown format")
	}
}

func TestStreamCaptureWritesOnlyMediaBytes(t *testing.T) {
	const media = "\x00\x00\x00\x18ftypmp42"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/display/displays/HDMI-A-1/screen.mp4" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "video/mp4" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		io.WriteString(w, media)
	}))
	defer server.Close()

	var out strings.Builder
	err := streamCapture(context.Background(), server.Client(), server.URL, "secret",
		captureTarget{Name: "HDMI-A-1", Format: "mp4"}, &out)
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
		io.WriteString(w, "you may not look at this screen")
	}))
	defer server.Close()

	var out strings.Builder
	err := streamCapture(context.Background(), server.Client(), server.URL, "",
		captureTarget{Name: "HDMI-A-1", Format: "mp4"}, &out)
	if err == nil {
		t.Fatal("streamCapture returned no error for a 403")
	}
	if !strings.Contains(err.Error(), "you may not look at this screen") {
		t.Fatalf("error %q does not carry the server's words", err)
	}
	if out.Len() != 0 {
		t.Fatalf("out carried %q on an error", out.String())
	}
}

func TestRunCaptureRejectsBadOptionsBeforeTheCluster(t *testing.T) {
	err := runCapture(context.Background(), nil,
		captureOptions{Name: "HDMI-A-1", Format: "gif"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runCapture reached the cluster with an unknown format")
	}
}

func TestCaptureClientNamesTheServiceDNS(t *testing.T) {
	client := captureClient(nil, nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", client.Transport)
	}
	if transport.TLSClientConfig.ServerName != apiServiceDNS {
		t.Fatalf("ServerName = %q, want %q", transport.TLSClientConfig.ServerName, apiServiceDNS)
	}
}
