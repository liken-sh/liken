package main

// PHD2 reads each guide frame from the telescope's INDI server, about
// once a second, so the guide camera's pod should run on the server's
// node. The camera of the OpticalTrain that a Guider names gets a
// required pod affinity to its telescope's server pod. The affinity
// points from the camera to the server because PowerOn creates the
// server first, and the Switch that powers the camera runs on that
// server. An affinity from the server to the camera would make the
// server wait for a camera that waits for power from the server.
//
// A guide camera with a claim gets no affinity, because its claimed
// device decides its node, and an affinity to a server on another node
// would leave the pod Pending.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// hostnameKey as a pod affinity's topologyKey means "the same node".
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

// guides reports whether a device is the camera that a Guider of the
// device's telescope guides with. For a device with a record it returns
// the value saved in the record, so an edit to the Guider during a
// session does not move the camera's pod (records.go).
func (t *tree) guides(server serverRef, d *device) bool {
	if d.record != nil {
		return d.record.Guides
	}
	if d.kind != observatory.CameraKind || server.kind != observatory.TelescopeKind || d.object.Spec.OpticalTrain == "" {
		return false
	}
	for _, guider := range t.guidersOf(server.name) {
		if guider.Spec.OpticalTrain == d.object.Spec.OpticalTrain {
			return true
		}
	}
	return false
}

// placement returns the pod affinity of a device's pod, which is nil
// for every device except a guide camera with no claim.
func placement(server serverRef, d *device, guides bool) *affinity {
	if !guides || len(d.object.Spec.Claim) > 0 {
		return nil
	}
	return besideServer(server)
}

// besideServer returns an affinity that places a pod on the node of the
// server's pod.
func besideServer(server serverRef) *affinity {
	return &affinity{PodAffinity: &podAffinity{Required: []podAffinityTerm{{
		LabelSelector: &labelSelector{MatchLabels: map[string]string{labelName: server.String()}},
		TopologyKey:   hostnameKey,
	}}}}
}
