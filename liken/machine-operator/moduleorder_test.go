package main

import (
	"strings"
	"testing"
)

// The boot loads modules in the order of spec.modules, so only a reboot
// applies a new order. A reorder leaves nothing wrong on the running
// machine, so decideConvergence stages the manifest for the next boot
// and does not request a reboot turn.
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
				t.Fatalf("condition is %+v, want reason %s", conv.condition, c.reason)
			}
			if conv.stage != c.stage || conv.requestLoad != c.load {
				t.Errorf("stage is %v and load is %v, want %v and %v", conv.stage, conv.requestLoad, c.stage, c.load)
			}
			if conv.requestReboot || conv.requestRestart || conv.pending != nil {
				t.Errorf("convergence is %+v, want no reboot, restart, or pending request", conv)
			}
			if !strings.Contains(conv.condition.Message, c.message) {
				t.Errorf("the message is %q, want it to contain %q", conv.condition.Message, c.message)
			}
			if strings.Contains(conv.condition.Message, "no longer declared") {
				t.Errorf("the message is %q, want no mention of a retraction", conv.condition.Message)
			}
		})
	}
}

// A live load promotes the manifest but cannot change the order the
// boot loaded modules in. So after a live load, the hash matches while
// the order differs, and decideConvergence must not report BootMismatch.
func TestAnOrderDifferenceUnderAMatchingHashIsNoContradiction(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"snd_hda_codec_alc269", "snd_hda_intel"}
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatalf("rendering the manifest of %s: %v", m.Metadata.Name, err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash
	facts.Boot.Modules = []string{"snd_hda_intel", "snd_hda_codec_alc269"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "StagedForNextBoot" {
		t.Fatalf("condition is %+v, want reason StagedForNextBoot", conv.condition)
	}
	if !conv.stage {
		t.Errorf("convergence is %+v, want the manifest staged for the boot that can load the new order", conv)
	}
}
