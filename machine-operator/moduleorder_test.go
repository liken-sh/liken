package main

// The convergence decisions for the order spec.modules declares. They
// live in their own file beside the parameter decisions
// (moduleparameters_test.go) for the same reason: converge_test.go is
// full.

import (
	"strings"
	"testing"
)

// The order the boot loads modules in is part of what the spec
// declares. Only a boot can change it, and nothing is wrong on the
// machine while it waits, so a reorder stages the manifest and asks
// for no turn.
func TestDecideConvergenceOnAModuleReorder(t *testing.T) {
	cases := []struct {
		name     string
		declared []string
		booted   []string
		reason   string
		stage    bool
		load     bool
		message  string
	}{
		{
			name:     "a pure reorder stages for the next boot",
			declared: []string{"snd_hda_codec_alc269", "snd_hda_intel"},
			booted:   []string{"snd_hda_intel", "snd_hda_codec_alc269"},
			reason:   "StagedForNextBoot",
			stage:    true,
			message:  "the declared order changed: snd_hda_codec_alc269 now loads before snd_hda_intel",
		},
		{
			name:     "a reorder beside an addition still loads the addition live",
			declared: []string{"snd_hda_codec_alc269", "snd_hda_intel", "btusb"},
			booted:   []string{"snd_hda_intel", "snd_hda_codec_alc269"},
			reason:   "LoadRequested",
			stage:    true,
			load:     true,
			message:  "the declared order changed: snd_hda_codec_alc269 now loads before snd_hda_intel",
		},
		{
			name:     "the same list in the same order is converged",
			declared: []string{"snd_hda_codec_alc269", "snd_hda_intel"},
			booted:   []string{"snd_hda_codec_alc269", "snd_hda_intel"},
			reason:   "Converged",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := labMachine()
			m.Spec.Modules = c.declared
			facts := labFacts()
			facts.Boot.Modules = c.booted
			conv := decideConvergence(m, facts, nil, "", turnStandalone)
			if conv.condition.Reason != c.reason {
				t.Fatalf("got %+v", conv.condition)
			}
			if conv.stage != c.stage || conv.requestLoad != c.load {
				t.Errorf("stage %v and load %v: %+v", conv.stage, conv.requestLoad, conv)
			}
			if conv.requestReboot || conv.requestRestart || conv.pending != nil {
				t.Errorf("a reorder asks for no disruption: %+v", conv)
			}
			if !strings.Contains(conv.condition.Message, c.message) {
				t.Errorf("the message should carry the diff: %q", conv.condition.Message)
			}
			if strings.Contains(conv.condition.Message, "no longer declared") {
				t.Errorf("a reorder is no retraction: %q", conv.condition.Message)
			}
		})
	}
}

// A live load promotes the manifest while the running kernel keeps
// the order it booted with. An order difference under a matching hash
// is what that load leaves behind, not the contradiction the
// BootMismatch guard reports.
func TestAnOrderDifferenceUnderAMatchingHashIsNoContradiction(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"snd_hda_codec_alc269", "snd_hda_intel"}
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"snd_hda_intel", "snd_hda_codec_alc269"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "StagedForNextBoot" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage {
		t.Errorf("the reorder stages for the boot that can load it: %+v", conv)
	}
}
