package main

import (
	"testing"
)

func TestTheHealthReportShowsTheFirstProblem(t *testing.T) {
	claim := claimReference{namespace: "home", name: "config"}
	for _, c := range []struct {
		name        string
		vol         *volume
		abnormal    bool
		wantMessage string
	}{
		{
			name:        "a tree on its ref",
			vol:         &volume{attributes: &attributes{ref: "main"}, commit: "d633176146e997"},
			abnormal:    false,
			wantMessage: "main at d633176",
		},
		{
			name: "a fetch that failed",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				fetchFault: "the forge is not there",
			},
			abnormal:    true,
			wantMessage: "the forge is not there",
		},
		{
			name: "a push that failed",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				pushFault:  "the remote refused the push",
			},
			abnormal:    true,
			wantMessage: "the remote refused the push",
		},
		{
			name: "an upstream that moved",
			vol: &volume{
				attributes:    &attributes{ref: "main"},
				commit:        "d633176146e997",
				upstreamMoved: "upstream moved: main is at 9b1c0de",
			},
			abnormal:    true,
			wantMessage: "upstream moved: main is at 9b1c0de",
		},
		{
			name: "an unarmed volume with work pending",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				kind:       writeableVolume,
				claim:      claim,
				pending:    []change{{path: "a.txt"}, {path: "b.txt"}},
			},
			abnormal:    true,
			wantMessage: "Unarmed: 2 paths pending, and the claim home/config sets no class",
		},
		{
			name: "an armed volume with work pending",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				kind:       writeableVolume,
				armed:      true,
				claim:      claim,
				pending:    []change{{path: "a.txt"}},
			},
			abnormal:    false,
			wantMessage: "main at d633176, 1 paths pending",
		},
		{
			name: "a ref the remote no longer has",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				kind:       writeableVolume,
				refDeleted: true,
			},
			abnormal:    true,
			wantMessage: "RefDeleted: the remote has no ref main",
		},
		{
			name: "a volume on its side branch",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				kind:       writeableVolume,
				diverged:   "main.config",
			},
			abnormal:    true,
			wantMessage: "Diverged: the tree pushes to main.config, not main",
		},
		{
			name: "a class the driver cannot read",
			vol: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				kind:       writeableVolume,
				claim:      claim,
				invalid:    "the VolumeAttributesClass config-eager is not valid: metadata: \"yes\" is not true or false",
			},
			abnormal:    true,
			wantMessage: "the VolumeAttributesClass config-eager is not valid: metadata: \"yes\" is not true or false",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			abnormal, message := c.vol.health()
			if abnormal != c.abnormal || message != c.wantMessage {
				t.Errorf("report() = %v, %q, want %v, %q",
					abnormal, message, c.abnormal, c.wantMessage)
			}
		})
	}
}

func TestTheSideBranchLastsUntilTheVolumePushesToItsRefAgain(t *testing.T) {
	vol := &volume{attributes: &attributes{ref: "main"}}
	if got := vol.sideBranch(); got != "" {
		t.Errorf("sideBranch() on a volume that pushes to its ref is %q, want empty", got)
	}
	vol.reportDiverged("main.config")
	if got := vol.sideBranch(); got != "main.config" {
		t.Errorf("sideBranch() after reportDiverged is %q, want main.config", got)
	}
	vol.reportReturnedToRef()
	if got := vol.sideBranch(); got != "" {
		t.Errorf("sideBranch() after reportHealed is %q, want empty", got)
	}
}

func TestAFetchThatFindsTheRefClearsTheDeletedRef(t *testing.T) {
	vol := &volume{attributes: &attributes{ref: "main"}}
	vol.reportRefDeleted("d633176146e997")
	if !vol.refIsDeleted() {
		t.Error("refIsDeleted() is false after reportRefDeleted, want true")
	}
	vol.reportCommit("d633176146e997")
	if vol.refIsDeleted() {
		t.Error("refIsDeleted() is true after a commit was reported from a fetch of the ref, want false")
	}
}

func TestTheAuthorComesFromTheClassOnAnArmedVolume(t *testing.T) {
	vol := &volume{}
	if got := vol.authorEnv(); got[0] != "GIT_AUTHOR_NAME="+defaultAuthorName {
		t.Errorf("the author of a volume with no class is %q, want the driver's default name", got[0])
	}
	vol.reportArmed(claimReference{}, "config-eager", &policy{authorName: "The lab"}, "")
	if got := vol.authorEnv(); got[0] != "GIT_AUTHOR_NAME=The lab" {
		t.Errorf("the author of a volume with a class is %q, want the author of the class", got[0])
	}
}

func TestReportPendingReturnsTrueOnlyWhenTheSetBecomesNotEmpty(t *testing.T) {
	vol := &volume{}
	if vol.reportPending(nil) {
		t.Error("reportPending() returned true for an empty set, want false")
	}
	if !vol.reportPending([]change{{path: "a.txt"}}) {
		t.Error("reportPending() returned false for the first non-empty set, want true")
	}
	if vol.reportPending([]change{{path: "a.txt"}, {path: "b.txt"}}) {
		t.Error("reportPending() returned true for a second non-empty set, want false")
	}
	if _, _, count := vol.gaugeValues(); count != 2 {
		t.Errorf("the volume has %d pending paths, want 2", count)
	}
}

func TestReportArmedReturnsTrueOnlyWhenTheArmedStateChanges(t *testing.T) {
	vol := &volume{}
	claim := claimReference{namespace: "home", name: "config"}
	if vol.reportArmed(claim, "", nil, "") {
		t.Error("reportArmed() returned true for a volume that was never armed, want false")
	}
	if !vol.reportArmed(claim, "config-eager", &policy{}, "") {
		t.Error("reportArmed() returned false when a class armed the volume, want true")
	}
	if vol.reportArmed(claim, "config-eager", &policy{}, "") {
		t.Error("reportArmed() returned true for the same class again, want false")
	}
	if !vol.reportArmed(claim, "", nil, "") {
		t.Error("reportArmed() returned false when the class was removed, want true")
	}
}

func TestThePodIsTheOneNamedInThePublish(t *testing.T) {
	vol := &volume{}
	if got := vol.podRef(); got != (podReference{}) {
		t.Errorf("podRef() on a staged volume is %+v, want empty", got)
	}
	pod := podReference{name: "writer", namespace: "home", uid: "9b1c"}
	vol.setPod(pod)
	if got := vol.podRef(); got != pod {
		t.Errorf("podRef() after setPod is %+v, want %+v", got, pod)
	}
}
