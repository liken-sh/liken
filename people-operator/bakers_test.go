package main

// These tests run the operator against the fake API server and play
// the kubelet: they finish each baker pod with the line that the bake
// command wrote for a file on disk.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// bakedLine runs the bake command against a file on disk, and answers
// the line it wrote, the line a baker pod's log holds.
func bakedLine(t *testing.T, picture []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "ada.png")
	if err := os.WriteFile(file, picture, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	bake(&out, file, personColour("ada"))
	return out.String()
}

// Each file scheme starts a baker pod in the namespace that can mount
// the file, with the file's volume, and with no credential.
func TestAFileSourceStartsABakerPod(t *testing.T) {
	cases := []struct {
		name          string
		avatar        string
		wantNamespace string
		wantVolume    volume
		wantFile      string
	}{
		{
			name:          "nfs",
			avatar:        "nfs://nas.example/export/people/ada.png",
			wantNamespace: operatorNS,
			wantVolume:    volume{Name: "source", NFS: &nfsVolume{Server: "nas.example", Path: "/export/people", ReadOnly: true}},
			wantFile:      "/source/ada.png",
		},
		{
			name:          "claim",
			avatar:        "claim://media/pictures/people/ada.png",
			wantNamespace: "media",
			wantVolume:    volume{Name: "source", PersistentVolumeClaim: &claimVolume{ClaimName: "pictures", ReadOnly: true}},
			wantFile:      "/source/people/ada.png",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada(c.avatar))
				w.settle()

				baker, started := w.api.bakerPod(t, c.wantNamespace, "ada")
				if !started {
					t.Fatalf("no baker pod in %s", c.wantNamespace)
				}
				spec := baker.Spec
				if len(spec.Volumes) != 1 || string(mustJSON(spec.Volumes[0])) != string(mustJSON(c.wantVolume)) {
					t.Errorf("volumes = %s, want %s", mustJSON(spec.Volumes), mustJSON(c.wantVolume))
				}
				wantArgs := []string{"bake", c.wantFile, colourArgument(personColour("ada"))}
				if got := spec.Containers[0].Args; !slices.Equal(got, wantArgs) {
					t.Errorf("args = %q, want %q", got, wantArgs)
				}
				if spec.Containers[0].Image != operatorImage {
					t.Errorf("image = %q, want the operator's own, %q", spec.Containers[0].Image, operatorImage)
				}
				if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
					t.Error("the baker pod mounts a service account token")
				}
				if !spec.Containers[0].VolumeMounts[0].ReadOnly {
					t.Error("the baker pod mounts the source for writing")
				}
			})
		})
	}
}

// A baker pod's result becomes the status, and the pod is deleted.
func TestABakerPodsResultBecomesTheStatus(t *testing.T) {
	cases := []struct {
		name       string
		phase      string
		log        func(t *testing.T) string
		wantStatus conditions.Status
		wantReason string
	}{
		{"a picture", podSucceeded, func(t *testing.T) string { return bakedLine(t, solid(t, 64, 64, green)) }, "True", reasonBaked},
		{"a file that is no picture", podFailed, func(t *testing.T) string { return bakedLine(t, []byte("not a picture")) }, "False", reasonDecodeFailed},
		{"a file that is gone", podFailed, func(t *testing.T) string {
			var out bytes.Buffer
			bake(&out, filepath.Join(t.TempDir(), "gone.png"), blue)
			return out.String()
		}, "False", reasonBakeFailed},
		{"a pod that wrote nothing", podFailed, func(*testing.T) string { return "" }, "False", reasonBakeFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada("claim://media/pictures/ada.png"))
				w.settle()

				w.api.finishPod("media", bakerPodName("ada"), c.phase, c.log(t))
				w.settle()

				got := w.api.person(t, "ada")
				if ready := got.Status.ready(); ready.Status != c.wantStatus || ready.Reason != c.wantReason {
					t.Errorf("AvatarReady = %+v, want %s, %s", ready, c.wantStatus, c.wantReason)
				}
				if _, held := w.api.bakerPod(t, "media", "ada"); held {
					t.Error("the baker pod stays after its result")
				}
			})
		})
	}
}

// A baker pod that does not finish in two minutes is deleted, and the
// condition says so.
func TestABakerPodThatDoesNotFinishIsDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada("nfs://nas.example/people/ada.png"))
		w.settle()

		time.Sleep(bakeDeadline)
		w.settle()

		if _, held := w.api.bakerPod(t, operatorNS, "ada"); held {
			t.Error("the baker pod stays after its deadline")
		}
		got := w.api.person(t, "ada")
		if ready := got.Status.ready(); ready.Status != "False" || ready.Reason != reasonBakeFailed {
			t.Errorf("AvatarReady = %+v, want False, BakeFailed", ready)
		}
		if !isInitials(got.Status.Thumbnail) {
			t.Error("a Person with no picture yet does not show the initials")
		}
	})
}

// The slow check starts a baker pod for each file source, and writes
// the status only when the file's time or size changed.
func TestTheSlowCheckBakesAFileAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		line := bakedLine(t, solid(t, 64, 64, green))
		w.api.putPerson(ada("claim://media/pictures/ada.png"))
		w.settle()
		w.api.finishPod("media", bakerPodName("ada"), podSucceeded, line)
		w.settle()
		before := w.api.person(t, "ada")

		time.Sleep(recheckInterval)
		w.settle()
		if _, started := w.api.bakerPod(t, "media", "ada"); !started {
			t.Fatal("the slow check started no baker pod")
		}
		w.api.finishPod("media", bakerPodName("ada"), podSucceeded, line)
		w.settle()

		if after := w.api.person(t, "ada"); after.Metadata.ResourceVersion != before.Metadata.ResourceVersion {
			t.Errorf("the status was written for a file that did not change")
		}
		if _, held := w.api.bakerPod(t, "media", "ada"); held {
			t.Error("the baker pod stays after its result")
		}
	})
}

// A baker pod whose Person is gone, or whose Person names another
// source now, is deleted. An operator that starts after the change
// finds the pod the same way.
func TestAStaleBakerPodIsDeleted(t *testing.T) {
	cases := []struct {
		name    string
		persons []person
	}{
		{"a Person that is gone", nil},
		{"a Person with another source", []person{ada("")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				stale := ada("claim://media/pictures/old.png")
				mount, _ := claimMount(mustParse(t, stale.Spec.Avatar), "")
				baker := buildBakerPod(&stale, mount, operatorImage)
				baker.Metadata.CreationTimestamp = time.Now().UTC().Format(time.RFC3339)
				for _, p := range c.persons {
					w.api.putPerson(p)
				}
				w.api.putPod(baker)
				w.settle()

				if _, held := w.api.bakerPod(t, "media", "ada"); held {
					t.Error("the stale baker pod stays")
				}
			})
		})
	}
}

// A malformed file URI shows the initials, and the condition says why.
func TestAMalformedFileSourceShowsTheInitials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada("claim://media"))
		w.settle()

		got := w.api.person(t, "ada")
		if ready := got.Status.ready(); ready.Status != "False" || ready.Reason != reasonBakeFailed {
			t.Errorf("AvatarReady = %+v, want False, BakeFailed", ready)
		}
		if !isInitials(got.Status.Thumbnail) {
			t.Error("the thumbnail is not the initials")
		}
	})
}

// A claim in a namespace that does not exist cannot be mounted, so the
// operator reports it once instead of trying to start the pod again.
func TestAClaimInANamespaceThatDoesNotExistFailsTheBake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.dropNamespace("gone")
		w.api.putPerson(ada("claim://gone/pictures/ada.png"))
		w.settle()

		ready := w.api.person(t, "ada").Status.ready()
		if ready.Status != "False" || ready.Reason != reasonBakeFailed {
			t.Errorf("AvatarReady = %+v, want False, BakeFailed", ready)
		}
		if want := "the namespace gone does not exist"; !strings.Contains(ready.Message, want) {
			t.Errorf("the message %q does not say %q", ready.Message, want)
		}
	})
}

// The operator reads the image a baker pod runs from its own pod.
func TestTheOperatorReadsItsOwnImage(t *testing.T) {
	cases := []struct {
		name    string
		pod     string
		wantErr bool
	}{
		{"the operator's pod", operatorPodName, false},
		{"a pod that is gone", "gone", true},
		{"a pod with no operator container", "other", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				api.putPod(pod{Metadata: podMeta{Name: "other", Namespace: operatorNS}, Spec: podSpec{Containers: []container{{Name: "sidecar", Image: "sidecar"}}}})
				client := apiclient.New(apiservertest.Host, api.server.Client(), "")

				image, err := ownImage(client, operatorNS, c.pod)
				if (err != nil) != c.wantErr || (err == nil && image != operatorImage) {
					t.Errorf("ownImage = %q, %v, want the image %q or an error: %v", image, err, operatorImage, c.wantErr)
				}
			})
		})
	}
}
