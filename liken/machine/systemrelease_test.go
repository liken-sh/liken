package machine

import (
	"strings"
	"testing"
)

func TestSystemReleaseRendersItsOwnIdentity(t *testing.T) {
	raw, hash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("rendering the record: %v", err)
	}
	if hash != ManifestHash(raw) {
		t.Error("the hash differs from ManifestHash of the rendered bytes")
	}

	again, sameHash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("ab", 32))
	if err != nil || sameHash != hash || string(again) != string(raw) {
		t.Error("rendering the same arguments twice gave different bytes or hashes, want the same")
	}
	_, otherHash, err := RenderSystemRelease("0.2.0", "B", "sha256:"+strings.Repeat("cd", 32))
	if err != nil || otherHash == hash {
		t.Error("a different release digest gave the same hash, want a different one")
	}

	record, err := ParseSystemRelease(raw)
	if err != nil {
		t.Fatalf("parsing the rendered record: %v", err)
	}
	if record.Version != "0.2.0" || record.Slot != "B" {
		t.Errorf("the parsed record is %+v, want version 0.2.0 and slot B", record)
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
				t.Error("the record parsed, want an error")
			}
		})
	}
}

func TestSystemReleasesIsItsOwnStore(t *testing.T) {
	root := t.TempDir()
	if err := SystemReleases(root).WriteStaged([]byte("system")); err != nil {
		t.Fatalf("writing the system release store: %v", err)
	}
	if raw, _ := MachineManifests(root).LoadStaged(); raw != nil {
		t.Error("the machine store read a record written to the system store, want none")
	}
	if raw, _ := SystemReleases(root).LoadStaged(); string(raw) != "system" {
		t.Error("the system store did not return the record written to it")
	}
}
