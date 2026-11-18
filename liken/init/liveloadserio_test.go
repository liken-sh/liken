package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// A live load applies an added serio entry the way it applies an added
// module: it records the entry in the boot record, promotes the staged
// manifest, and declares the entry to the serio registry.
func TestLiveLoadDeclaresAnAddedSerioEntry(t *testing.T) {
	staged := liveLoadable()
	staged.Serio = []machine.SerioAttachment{pulse8Entry}
	store, base, loader, hash := liveLoadFixture(t, staged, []string{"loop"})
	loader.serio = newSerioRegistry(newFakeTTYs(t).open)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash || !slices.Equal(boot.Serio, staged.Serio) {
		t.Errorf("the boot record is %+v, want the applied serio entry and the manifest hash", boot)
	}
	if got := loader.serio.declaredEntries(); !slices.Equal(got, staged.Serio) {
		t.Errorf("the declared entries are %+v, want %+v", got, staged.Serio)
	}
}

// A live load does not remove a serio entry, because pods can hold
// devices that the port created. The manifest stays staged until the
// next boot.
func TestLiveLoadRefusesASerioRetraction(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, liveLoadable(), []string{"loop"})
	loader.bootSerio = []machine.SerioAttachment{pulse8Entry}
	loader.serio = newSerioRegistry(newFakeTTYs(t).open)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("the retracting manifest is not staged, want it staged until the next boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record is %+v, want it unchanged", boot)
	}
	if got := loader.serio.declaredEntries(); got != nil {
		t.Errorf("the declared entries are %+v, want none", got)
	}
}

func TestAppliedInPlaceNamesWhatTheLoadApplied(t *testing.T) {
	tests := []struct {
		name     string
		loaded   []string
		attached []machine.SerioAttachment
		want     string
	}{
		{"modules", []string{"cdc_acm", "serport"}, nil, "cdc_acm, serport loaded"},
		{"an entry", nil, []machine.SerioAttachment{pulse8Entry}, "pulse8-cec 2548:1002 declared for attachment"},
		{"both", []string{"pulse8_cec"}, []machine.SerioAttachment{pulse8Entry},
			"pulse8_cec loaded; pulse8-cec 2548:1002 declared for attachment"},
		{"nothing", nil, nil, "nothing to load"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := appliedInPlace(test.loaded, test.attached); got != test.want {
				t.Errorf("appliedInPlace = %q, want %q", got, test.want)
			}
		})
	}
}
