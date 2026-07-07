package machine

import (
	"strings"
	"testing"
)

func TestSystemReleaseRendersItsOwnIdentity(t *testing.T) {
	raw, hash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if hash != ManifestHash(raw) {
		t.Error("the hash must be the hash of the rendered bytes")
	}

	again, sameHash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("ab", 32))
	if err != nil || sameHash != hash || string(again) != string(raw) {
		t.Error("the same decision must always render the same bytes")
	}
	_, otherHash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("cd", 32))
	if err != nil || otherHash == hash {
		t.Error("a different catalog digest is a different decision")
	}

	record, err := ParseSystemRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != "0.2.0" || record.Slot != "B" {
		t.Errorf("round trip: %+v", record)
	}
}

func TestParseSystemReleaseVetsAtTheDoor(t *testing.T) {
	cases := map[string]string{
		"wrong kind":    `{kind: Machine, version: 0.2.0, slot: B}`,
		"no version":    `{kind: SystemRelease, slot: B}`,
		"bad slot":      `{kind: SystemRelease, version: 0.2.0, slot: C}`,
		"unknown field": `{kind: SystemRelease, version: 0.2.0, slot: B, surprise: true}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSystemRelease([]byte(doc)); err == nil {
				t.Error("expected a parse error")
			}
		})
	}
}

func TestSystemReleasesIsItsOwnStore(t *testing.T) {
	root := t.TempDir()
	if err := SystemReleases(root).WriteStaged([]byte("system")); err != nil {
		t.Fatal(err)
	}
	if raw, _ := MachineManifests(root).LoadStaged(); raw != nil {
		t.Error("the system store must not collide with the machine's")
	}
	if raw, _ := SystemReleases(root).LoadStaged(); string(raw) != "system" {
		t.Error("the system store must read its own writes")
	}
}
