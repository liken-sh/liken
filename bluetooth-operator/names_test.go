package main

import "testing"

func TestDeviceNameAndBack(t *testing.T) {
	cases := []struct {
		address string
		name    string
	}{
		{"A0:AB:51:33:B7:12", "a0-ab-51-33-b7-12"},
		{"a0:ab:51:33:b7:12", "a0-ab-51-33-b7-12"},
		{"  A0:AB:51:33:B7:12  ", "a0-ab-51-33-b7-12"},
		{"00:00:00:00:00:00", "00-00-00-00-00-00"},
	}
	for _, c := range cases {
		t.Run(c.address, func(t *testing.T) {
			name := deviceName(c.address)
			if name != c.name {
				t.Fatalf("deviceName(%q) = %q, want %q", c.address, name, c.name)
			}
			back := macFromDeviceName(name)
			if back != normalizeMAC(c.address) {
				t.Fatalf("macFromDeviceName(%q) = %q, want %q", name, back, normalizeMAC(c.address))
			}
		})
	}
}

func TestPublishedMAC(t *testing.T) {
	cases := []struct{ address, want string }{
		{"a0:ab:51:33:b7:12", "A0:AB:51:33:B7:12"},
		{"A0:AB:51:33:B7:12", "A0:AB:51:33:B7:12"},
	}
	for _, c := range cases {
		if got := publishedMAC(c.address); got != c.want {
			t.Errorf("publishedMAC(%q) = %q, want %q", c.address, got, c.want)
		}
	}
}

func TestValidMAC(t *testing.T) {
	cases := []struct {
		address string
		valid   bool
	}{
		{"a0:ab:51:33:b7:12", true},
		{"A0:AB:51:33:B7:12", true},
		{"", false},
		{"a0:ab:51:33:b7", false},
		{"a0:ab:51:33:b7:12:99", false},
		{"a0-ab-51-33-b7-12", false},
		{"g0:ab:51:33:b7:12", false},
		{"a:ab:51:33:b7:12", false},
	}
	for _, c := range cases {
		if got := validMAC(c.address); got != c.valid {
			t.Errorf("validMAC(%q) = %v, want %v", c.address, got, c.valid)
		}
	}
}
