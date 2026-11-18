package main

// This file reads from the API server which output devices a claim
// holds.
//
// The Display pass restarts the compositor to return a screen to its
// declared mode or to repair a canvas. A restart takes the screen away
// from the claim that uses it, so both wait while a claim holds the
// screen. The CDI specs on disk cover only the claims that the kubelet
// has prepared. A claim whose prepare has not succeeded has no spec,
// but the kubelet retries the prepare until the pod starts or is
// deleted, and each attempt switches the screen to the claim's mode.
// A pass that read only the specs would switch the screen back between
// retries, and each switch restarts the compositor and lengthens the
// kubelet's crash backoff.
//
// So a claim holds an output when its allocation includes the output
// in this node's pool and it is reserved for a pod that is not being
// deleted. ResourceClaims have no field selector for the node, so the
// check lists every claim in the cluster. The Display pass lists them
// only when a restart would follow, at most once per pass. The slice
// pass lists them only while no compositor serves, so that it does not
// taint an output whose claim is still preparing (compositorDown in
// slices.go).

import (
	"errors"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// allocatedOutputs returns the output devices of this node that a claim
// holds through a live pod. It leaves out draw and control devices, as
// preparedOutputs does, because neither owns the mode.
func allocatedOutputs(client *apiclient.Client, node string, stores clusterStores) (map[string]bool, error) {
	claims, err := listResourceClaims(client)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, claim := range claims {
		outputs := claimOutputs(claim, node)
		if len(outputs) == 0 {
			continue
		}
		live, err := heldByLivePod(client, stores, claim)
		if err != nil {
			return nil, err
		}
		if !live {
			continue
		}
		for _, output := range outputs {
			held[output] = true
		}
	}
	return held, nil
}

// claimOutputs returns the output devices that one claim has allocated
// from this driver's pool on this node.
func claimOutputs(claim ResourceClaim, node string) []string {
	if claim.Status.Allocation == nil {
		return nil
	}
	var outputs []string
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != DriverName || result.Pool != node {
			continue
		}
		if _, control := outputOfControl(result.Device); control {
			continue
		}
		if _, draw := outputOfDraw(result.Device); draw {
			continue
		}
		outputs = append(outputs, result.Device)
	}
	return outputs
}

// heldByLivePod reports whether the claim is reserved for a pod that
// exists and is not being deleted. It reads the pod and does not trust
// status.reservedFor alone, because a reservation can outlast its pod,
// and the kubelet stops retrying the prepare once the pod is gone.
func heldByLivePod(client *apiclient.Client, stores clusterStores, claim ResourceClaim) (bool, error) {
	for _, consumer := range claim.Status.ReservedFor {
		if consumer.Resource != podsResource {
			continue
		}
		pod, err := stores.pod(client, claim.Metadata.Namespace, consumer.Name)
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if pod.Metadata.DeletionTimestamp == nil {
			return true, nil
		}
	}
	return false, nil
}

// preparingOutputs returns the output devices that a claim holds and
// that no CDI spec on disk lists yet.
func preparingOutputs(allocated, prepared map[string]bool) map[string]bool {
	preparing := map[string]bool{}
	for output := range allocated {
		if !prepared[output] {
			preparing[output] = true
		}
	}
	return preparing
}

// screenHolds reports, for one pass, which output devices a claim
// holds. It checks the prepared specs first, which need no request, and
// calls allocated, at most once, only when the specs show no claim.
type screenHolds struct {
	prepared  map[string]bool
	allocated func() (map[string]bool, error)

	read    bool
	claimed map[string]bool
	err     error
}

// holds reports whether a claim holds one output device.
func (h *screenHolds) holds(device string) (bool, error) {
	if h.prepared[device] {
		return true, nil
	}
	claimed, err := h.claims()
	return claimed[device], err
}

// any reports whether a claim holds any output device of this card.
func (h *screenHolds) any() (bool, error) {
	if len(h.prepared) > 0 {
		return true, nil
	}
	claimed, err := h.claims()
	return len(claimed) > 0, err
}

func (h *screenHolds) claims() (map[string]bool, error) {
	if !h.read && h.allocated != nil {
		h.claimed, h.err = h.allocated()
		h.read = true
	}
	return h.claimed, h.err
}
