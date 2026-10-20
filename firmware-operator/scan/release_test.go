package main

import "testing"

func TestToRelease(t *testing.T) {
	sha1 := "66b3c5930d4736f95a01f88b440d9060554f2036"
	sha256 := "9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5"
	tests := []struct {
		name string
		in   fwupdRelease
		want Release
	}{
		{
			"the first location and the SHA-256",
			fwupdRelease{Version: "1", Urgency: "high", Checksum: []string{sha1, sha256}, Locations: []string{"https://a", "https://b"}, Size: 5},
			Release{Version: "1", Urgency: "high", Cabinet: &Cabinet{URL: "https://a", SHA256: sha256, Size: 5}},
		},
		{
			"only a SHA-1",
			fwupdRelease{Version: "1", Checksum: []string{sha1}, Locations: []string{"https://a"}},
			Release{Version: "1", Cabinet: &Cabinet{URL: "https://a"}},
		},
		{
			"no location",
			fwupdRelease{Version: "1", Checksum: []string{sha256}},
			Release{Version: "1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toRelease(tt.in)

			if got.Version != tt.want.Version || got.Urgency != tt.want.Urgency {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if (got.Cabinet == nil) != (tt.want.Cabinet == nil) || (got.Cabinet != nil && *got.Cabinet != *tt.want.Cabinet) {
				t.Errorf("cabinet %+v, want %+v", got.Cabinet, tt.want.Cabinet)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	withUpdate := Device{Releases: []Release{{Version: "1"}}}
	tests := []struct {
		devices []Device
		want    string
	}{
		{nil, "0 devices, no updates"},
		{[]Device{{}}, "1 device, no updates"},
		{[]Device{withUpdate, {}, withUpdate}, "3 devices, 2 with updates"},
	}
	for _, tt := range tests {
		if got := summary(tt.devices); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}
