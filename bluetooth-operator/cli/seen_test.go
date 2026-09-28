package main

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func requestObject(phase, peripheral string, seen []map[string]any) *unstructured.Unstructured {
	entries := make([]any, len(seen))
	for index, device := range seen {
		entries[index] = device
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"phase":      phase,
			"peripheral": peripheral,
			"seen":       entries,
		},
	}}
}

func TestSeenFrom(t *testing.T) {
	object := requestObject("Open", "", []map[string]any{
		{"address": "A0:AB:51:33:B7:12", "name": "DualSense", "firstSeen": "2026-09-17T00:00:00Z"},
		{"address": "11:22:33:44:55:66"},
	})
	devices, phase, peripheral := seenFrom(object)
	if phase != "Open" || peripheral != "" {
		t.Fatalf("phase, peripheral = %q, %q", phase, peripheral)
	}
	if len(devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(devices))
	}
	if devices[0].Address != "A0:AB:51:33:B7:12" || devices[0].Name != "DualSense" {
		t.Fatalf("first device = %+v", devices[0])
	}
	if devices[1].Name != "" {
		t.Fatalf("second device name = %q, want empty", devices[1].Name)
	}
}

func TestSeenFromWithNoStatus(t *testing.T) {
	devices, phase, peripheral := seenFrom(&unstructured.Unstructured{Object: map[string]any{}})
	if len(devices) != 0 || phase != "" || peripheral != "" {
		t.Fatalf("empty object read as %v, %q, %q", devices, phase, peripheral)
	}
}

func TestRenderSeen(t *testing.T) {
	if got := renderSeen(nil); !strings.Contains(got, "no devices") {
		t.Fatalf("empty render = %q", got)
	}
	got := renderSeen([]seenDevice{
		{Address: "A0:AB:51:33:B7:12", Name: "DualSense"},
		{Address: "11:22:33:44:55:66"},
	})
	if !strings.Contains(got, "1) A0:AB:51:33:B7:12 DualSense") {
		t.Fatalf("render = %q", got)
	}
	if !strings.Contains(got, "2) 11:22:33:44:55:66 unnamed") {
		t.Fatalf("render = %q", got)
	}
}

func TestResolveSelection(t *testing.T) {
	devices := []seenDevice{
		{Address: "A0:AB:51:33:B7:12"},
		{Address: "11:22:33:44:55:66"},
	}
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"by index", "1", "A0:AB:51:33:B7:12", false},
		{"second by index", "2", "11:22:33:44:55:66", false},
		{"by address", "11:22:33:44:55:66", "11:22:33:44:55:66", false},
		{"by lowercase address", "a0:ab:51:33:b7:12", "A0:AB:51:33:B7:12", false},
		{"trims spaces", " 1 ", "A0:AB:51:33:B7:12", false},
		{"index out of range", "3", "", true},
		{"index zero", "0", "", true},
		{"empty", "", "", true},
		{"unknown address", "de:ad:be:ef:00:01", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSelection(tc.input, devices)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveSelection(%q) returned no error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSelection(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("resolveSelection(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
