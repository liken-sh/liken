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
		{
			name: "a ref the remote no longer holds",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				refDeleted: true,
			},
			abnormal: true,
			says:     "RefDeleted: the remote holds no main",
		},
		{
			name: "a volume on its side branch",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				diverged:   "main.config",
			},
			abnormal: true,
			says:     "Diverged: the tree pushes to main.config, not main",
		},
		{
			name: "a work tree the sweep kept",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				abandoned:  "the work tree of old holds unpushed commits and was unstaged 745h ago",
			},
			abnormal: true,
			says:     "the work tree of old holds unpushed commits and was unstaged 745h ago",
		},
		{
			name: "a class the driver cannot read",
			held: &volume{
				attributes: &attributes{ref: "main"},
				commit:     "d633176146e997",
				writeable:  true,
				claim:      claim,
				invalid:    "the class config-eager is not valid: metadata: \"yes\" is not true or false",
			},
			abnormal: true,
			says:     "the class config-eager is not valid: metadata: \"yes\" is not true or false",
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

func TestTheSideBranchIsHeldUntilAHeal(t *testing.T) {
	held := &volume{attributes: &attributes{ref: "main"}}
	if got := held.divergedFrom(); got != "" {
		t.Errorf("a volume on its ref names the branch %q, want none", got)
	}
	held.reportDiverged("main.config")
	if got := held.divergedFrom(); got != "main.config" {
		t.Errorf("the volume names the branch %q, want main.config", got)
	}
	held.reportHealed()
	if got := held.divergedFrom(); got != "" {
		t.Errorf("a healed volume names the branch %q, want none", got)
	}
}

func TestAFetchThatReachesTheRefEndsTheDeletedReport(t *testing.T) {
	held := &volume{attributes: &attributes{ref: "main"}}
	held.reportRefDeleted("d633176146e997")
	if !held.refIsDeleted() {
		t.Error("the volume does not report the ref the remote no longer holds")
	}
	held.reportCommit("d633176146e997")
	if held.refIsDeleted() {
		t.Error("a fetch that reached the ref still reports it deleted")
	}
}

func TestTheAuthorIsTheClassWhereAClassArmsTheVolume(t *testing.T) {
	held := &volume{}
	if got := held.authorEnv(); got[0] != "GIT_AUTHOR_NAME="+defaultAuthorName {
		t.Errorf("an unarmed volume commits as %q, want the driver's own name", got[0])
	}
	held.reportArmed(claimReference{}, "config-eager", &policy{authorName: "The lab"}, "")
	if got := held.authorEnv(); got[0] != "GIT_AUTHOR_NAME=The lab" {
		t.Errorf("an armed volume commits as %q, want the class's author", got[0])
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
	if held.reportArmed(claim, "", nil, "") {
		t.Error("a volume that was never armed changed hands")
	}
	if !held.reportArmed(claim, "config-eager", &policy{}, "") {
		t.Error("a volume the class took did not change hands")
	}
	if held.reportArmed(claim, "config-eager", &policy{}, "") {
		t.Error("a volume the same class holds changed hands")
	}
	if !held.reportArmed(claim, "", nil, "") {
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
