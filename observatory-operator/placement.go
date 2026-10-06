package main

// Where the guide camera's pod runs. PHD2 reads each guide frame from
// the telescope's INDI server, so a frame crosses the link between the
// camera's node and the server's node about once a second. Plan 03
// measured 430 ms for each hop of a guide-sized frame over a 76 Mbit/s
// link between two nodes, and the scheduler alone put the guide camera
// on the other node from the server.
//
// So the camera of the OpticalTrain that a Guider names has a required
// pod affinity to its telescope's server, and the scheduler places it
// on the server's node. The camera follows the server, not the reverse:
// PowerOn creates the server before the guide camera's pod exists, and
// the Switch that powers the camera runs on that server. A server that
// waited for the camera's node would wait forever.
//
// A guide camera with a claim gets no affinity. The node of its device
// decides where it runs, and an affinity to a server on another node
// would hold its pod Pending. Such a camera runs where its device is,
// and its frames cross the link when the server runs elsewhere.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// hostnameKey is the node label that the kubelet sets to the node's
// name. A pod affinity on it means "the same node".
const hostnameKey = "kubernetes.io/hostname"

type affinity struct {
	PodAffinity *podAffinity `json:"podAffinity,omitempty"`
}

type podAffinity struct {
	Required []podAffinityTerm `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

type podAffinityTerm struct {
	LabelSelector *labelSelector `json:"labelSelector"`
	TopologyKey   string         `json:"topologyKey"`
}

type labelSelector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

// guides reports whether a device is a camera that a Guider of the
// device's telescope guides with.
func (t *tree) guides(server serverRef, d *device) bool {
	if d.kind != observatory.CameraKind || server.kind != observatory.TelescopeKind || d.object.Spec.OpticalTrain == "" {
		return false
	}
	for _, guider := range t.guiders {
		if guider.Spec.Telescope == server.name && guider.Spec.OpticalTrain == d.object.Spec.OpticalTrain {
			return true
		}
	}
	return false
}

// placement answers the affinity of a device's pod: beside its server
// for a guide camera with no claim, and none for every other device.
func placement(server serverRef, d *device, guides bool) *affinity {
	if !guides || len(d.object.Spec.Claim) > 0 {
		return nil
	}
	return besideServer(server)
}

// besideServer answers the affinity that places a pod on the node of a
// server's pod.
func besideServer(server serverRef) *affinity {
	return &affinity{PodAffinity: &podAffinity{Required: []podAffinityTerm{{
		LabelSelector: &labelSelector{MatchLabels: map[string]string{labelName: server.String()}},
		TopologyKey:   hostnameKey,
	}}}}
}
