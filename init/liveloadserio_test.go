package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// An added serio entry applies in the same live load as an added
// module: the load records it, promotes the manifest, and declares it
// to the serio watch.
func TestLiveLoadDeclaresAnAddedSerioEntry(t *testing.T) {
	staged := liveLoadable()
	staged.Serio = []machine.SerioAttachment{pulse8Entry}
	store, base, loader, hash := liveLoadFixture(t, staged, []string{"loop"})
	loader.serio = newSerioRegistry(newFakeTTYs(t).open)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	boot := bootManifestRecord(t, loader)
	if boot.ManifestHash != hash || !slices.Equal(boot.Serio, staged.Serio) {
		t.Errorf("the boot record should carry the applied entry: %+v", boot)
	}
	if got := loader.serio.declaredEntries(); !slices.Equal(got, staged.Serio) {
		t.Errorf("declared = %+v", got)
	}
}

// A removed entry's holder keeps the port for the pods that hold its
// devices, so the removal waits for a boot.
func TestLiveLoadRefusesASerioRetraction(t *testing.T) {
	store, base, loader, hash := liveLoadFixture(t, liveLoadable(), []string{"loop"})
	loader.bootSerio = []machine.SerioAttachment{pulse8Entry}
	loader.serio = newSerioRegistry(newFakeTTYs(t).open)

	loader.apply(machine.ModulesIntent{ManifestHash: hash}, store, base)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a retracting manifest must stay staged for its boot")
	}
	if boot := bootManifestRecord(t, loader); boot.ManifestHash != "before" {
		t.Errorf("the boot record must be untouched: %+v", boot)
	}
	if got := loader.serio.declaredEntries(); got != nil {
		t.Errorf("declared = %+v", got)
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
				t.Errorf("got %q", got)
			}
		})
	}
}
