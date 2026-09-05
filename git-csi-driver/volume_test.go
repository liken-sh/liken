package main

import (
	"testing"
)

func TestTheConditionSaysWhatIsWrongFirst(t *testing.T) {
	claim := claimReference{namespace: "home", name: "config"}
	for _, c := range []struct {
		name     string
		held     *volume
		abnormal bool
		says     string
	}{
		{
			name:     "a tree on its ref",
			held:     &volume{attributes: &attributes{ref: "main"}, commit: "d633176146e997"},
			abnormal: false,
			says:     "main at d633176",
		},
		{
			name: "a fetch that failed",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				trouble:    "the forge is not there",
			},
			abnormal: true,
			says:     "the forge is not there",
		},
		{
			name: "an unarmed volume with work pending",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				claim:      claim,
				pending:    []change{{path: "a.txt"}, {path: "b.txt"}},
			},
			abnormal: true,
			says:     "unarmed: 2 paths pending, no class on claim home/config",
		},
		{
			name: "an armed volume with work pending",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				armed:      true,
				claim:      claim,
				pending:    []change{{path: "a.txt"}},
			},
			abnormal: false,
			says:     "main at d633176, 1 paths pending",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			abnormal, message := c.held.report()
			if abnormal != c.abnormal || message != c.says {
				t.Errorf("report answered %v, %q, want %v, %q",
					abnormal, message, c.abnormal, c.says)
			}
		})
	}
}

func TestThePendingSetIsFirstWhenItGoesFromEmpty(t *testing.T) {
	held := &volume{}
	if held.reportPending(nil) {
		t.Error("an empty set is the first work pending")
	}
	if !held.reportPending([]change{{path: "a.txt"}}) {
		t.Error("the first work pending is not reported as first")
	}
	if held.reportPending([]change{{path: "a.txt"}, {path: "b.txt"}}) {
		t.Error("more work pending is reported as first")
	}
	if _, _, count := held.reading(); count != 2 {
		t.Errorf("the volume holds %d pending paths, want 2", count)
	}
}

func TestTheArmedStateChangesOnlyWhenTheClassDoes(t *testing.T) {
	held := &volume{}
	claim := claimReference{namespace: "home", name: "config"}
	if held.reportArmed(claim, "", false) {
		t.Error("a volume that was never armed changed hands")
	}
	if !held.reportArmed(claim, "config-eager", true) {
		t.Error("a volume the class took did not change hands")
	}
	if held.reportArmed(claim, "config-eager", true) {
		t.Error("a volume the same class holds changed hands")
	}
	if !held.reportArmed(claim, "", false) {
		t.Error("a volume the class left did not change hands")
	}
}

func TestThePodComesFromThePublish(t *testing.T) {
	held := &volume{}
	if got := held.podRef(); got != (podReference{}) {
		t.Errorf("a staged volume names the pod %+v, want none", got)
	}
	pod := podReference{name: "writer", namespace: "home", uid: "9b1c"}
	held.setPod(pod)
	if got := held.podRef(); got != pod {
		t.Errorf("the volume names the pod %+v, want %+v", got, pod)
	}
}
