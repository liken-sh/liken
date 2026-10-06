package main

// The guide camera's pod runs on the node of its telescope's server.
// Plan 03 measured 430 ms for each hop of a guide-sized frame between
// two nodes, and the scheduler alone put the guide camera on the other
// node from the server.

import (
	"maps"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// besideEast is the pod affinity that holds a pod to the node of the
// east telescope's server.
var besideEast = &affinity{PodAffinity: &podAffinity{Required: []podAffinityTerm{{
	LabelSelector: &labelSelector{MatchLabels: map[string]string{labelName: "east-telescope"}},
	TopologyKey:   "kubernetes.io/hostname",
}}}}

// A guide camera with a claim runs on the node of its device, so an
// affinity to the server could hold it Pending forever.
func TestOnlyAGuideCameraWithNoClaimFollowsTheServer(t *testing.T) {
	t.Parallel()
	claimed := simulator(observatory.CameraKind, "east-guide", "indi_asi_ccd")
	claimed.object.Spec.Claim = []byte(`{"devices":{"requests":[{"name":"camera","exactly":{"deviceClassName":"usb.liken.sh"}}]}}`)
	cases := []struct {
		what   string
		d      *device
		guides bool
		want   *affinity
	}{
		{"the guide camera", simulator(observatory.CameraKind, "east-guide", "indi_simulator_guide"), true, besideEast},
		{"another camera", simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd"), false, nil},
		{"a guide camera with a claim", claimed, true, nil},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			p, _, _, err := devicePod("observatory", east, c.d, c.guides)
			if err != nil {
				t.Fatal(err)
			}
			if mustJSON(p.Spec.Affinity) != mustJSON(c.want) {
				t.Errorf("affinity = %s, want %s", mustJSON(p.Spec.Affinity), mustJSON(c.want))
			}
		})
	}
}

// affinities answers the pod affinity of each pod that has one, by the
// pod's name.
func affinities(w *world) map[string]*affinity {
	out := map[string]*affinity{}
	for _, name := range w.api.names(podsCollection) {
		if p, _ := decode[pod](w.t, w.api, podsCollection, name); p.Spec.Affinity != nil {
			out[name] = p.Spec.Affinity
		}
	}
	return out
}

func TestTheGuiderAndItsCameraRunBesideTheServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		what   string
		change func(w *world)
		want   map[string]*affinity
	}{
		{"the example", func(w *world) {}, map[string]*affinity{"east-guide-camera": besideEast, "east-guider": besideEast}},
		{"no Guider", func(w *world) {
			w.api.deleteNamed(kindCollection(observatory.GuiderKind), "east")
		}, map[string]*affinity{}},
		{"a Guider of another train", func(w *world) {
			w.put(observatory.GuiderKind, "east", map[string]any{"telescope": "east", "opticalTrain": "east-imaging", "pulses": "Mount"})
		}, map[string]*affinity{"east-main-camera": besideEast, "east-guider": besideEast}},
		{"a guide camera with a claim", func(w *world) {
			w.put(observatory.CameraKind, "east-guide", map[string]any{
				"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_guide"},
				"claim": map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "camera", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}},
			})
		}, map[string]*affinity{"east-guider": besideEast}},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.change(w)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
				if got := affinities(w); !maps.EqualFunc(got, c.want, func(a, b *affinity) bool { return mustJSON(a) == mustJSON(b) }) {
					t.Errorf("affinities = %s\nwant %s", mustJSON(got), mustJSON(c.want))
				}
			})
		})
	}
}
