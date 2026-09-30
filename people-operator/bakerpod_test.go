package main

import (
	"strings"
	"testing"
)

// A file URI that names no file makes no mount.
func TestAFileURIMustNameAFile(t *testing.T) {
	cases := []string{
		"nfs:///people/ada.png",
		"nfs://nas.example",
		"nfs://nas.example/",
		"claim://media",
		"claim://media/pictures",
		"claim://media/pictures/",
		"claim:///pictures/ada.png",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			ref := mustParse(t, raw)
			if _, err := mounts[ref.Scheme](ref, operatorNS); err == nil {
				t.Errorf("%s made a mount", raw)
			}
		})
	}
}

// A path that climbs out of the mount stays inside it.
func TestAFilePathStaysInsideItsMount(t *testing.T) {
	cases := map[string]string{
		"claim://media/pictures/../../etc/passwd": "/source/etc/passwd",
		"nfs://nas.example/people/../ada.png":     "/source/ada.png",
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			ref := mustParse(t, raw)
			mount, err := mounts[ref.Scheme](ref, operatorNS)
			if err != nil || mount.file != want {
				t.Errorf("the file is %q, %v, want %q", mount.file, err, want)
			}
		})
	}
}

// A baker pod's name fits the limit on a pod's name, and two long
// Person names give two pod names.
func TestABakerPodNameFits(t *testing.T) {
	long := strings.Repeat("a", 250)
	first, second := bakerPodName(long+"x"), bakerPodName(long+"y")
	if len(first) > bakerPodMaxNameSize || first == second {
		t.Errorf("the names are %q and %q, want two names of at most %d characters", first, second, bakerPodMaxNameSize)
	}
	if got := bakerPodName("ada"); got != "avatar-ada" {
		t.Errorf("bakerPodName(ada) = %q, want avatar-ada", got)
	}
}
