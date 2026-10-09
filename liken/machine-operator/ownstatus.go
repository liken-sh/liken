package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// publishOwnStatus is kubernetes.PublishStatus for the machine
// writing about itself, which is the one writer entitled to resolve
// a conflict, rather than give up on the write. A Machine's status
// has exactly two other writers: the rollout conductor, granting and
// reclaiming reboot turns, and the fleet sweep, marking silent
// machines Lost. If one of them wrote between this pass's read and
// its write, this machine's observations are still the freshest
// thing anyone has, because it observes the hardware directly. So
// the answer is to retry against a fresh read, rather than discard
// the pass. The merge honors each condition's owner. The
// conductor's grant carries over from the fresh copy exactly as
// written, present or absent, with its transition time untouched
// (the rollout's stall clock measures from that time), and every
// other field is this pass's own observation. A Lost verdict needs
// no special handling: overwriting it is exactly how a machine
// announces that it is back.
//
// before is the status the object carried when the pass began,
// rendered as the JSON a write would send. When this pass observed
// exactly that, the function writes nothing at all. A settled
// machine's report is the same on every pass, and sending it
// anyway would make the API server, and every etcd leader behind
// it, process a write that changes nothing. The kubelet applies the
// same restraint to Node status, and the machine's liveness does
// not depend on this write anyway, because that is the heartbeat
// lease's job. Skipping against a stale working copy is safe for
// the same reason every skipped event is safe: whatever made the
// server's copy differ arrives on the watch, and the pass it
// triggers sees the difference and writes.
//
// One retry is enough. A second conflict means the object is
// changing faster than this pass can read it, and the write that
// won the race is already queued on the watch, so the pass it
// triggers will publish moments from now.
func publishOwnStatus(r *reader, m *machine.Machine, status *machine.MachineStatus, before []byte) error {
	after, err := json.Marshal(status)
	if err == nil && bytes.Equal(before, after) {
		return nil
	}

	err = r.publishStatus(m, status)
	if !errors.Is(err, apiclient.ErrConflict) {
		return err
	}
	// This read goes to the API server, not to the watch's copy. It
	// follows a write that lost to another writer, and the copy can
	// still lag behind the write that won.
	fresh, gerr := r.freshMachine(m.Metadata.Name)
	if gerr != nil {
		return err
	}
	status.Conditions = api.RemoveCondition(slices.Clone(status.Conditions), machine.RebootApprovedCondition)
	if grant := api.FindCondition(fresh.Status.Conditions, machine.RebootApprovedCondition); grant != nil {
		status.Conditions = append(status.Conditions, *grant)
	}
	return r.publishStatus(fresh, status)
}
