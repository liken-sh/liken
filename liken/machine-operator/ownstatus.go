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

// publishOwnStatus writes the status that this pass observed, and
// when the write conflicts it merges and retries once.
//
// The cluster operator's sweep also writes a Machine's status: it
// grants and reclaims reboot turns, and it marks silent machines
// Lost. When it writes between this pass's read and its write, this
// pass's observations are still the newest, because the machine reads
// its hardware directly. So on a conflict the function reads the
// Machine again and writes this pass's status onto it, except for the
// RebootApproved condition. That condition belongs to the cluster
// operator, so the merge copies it from the fresh object unchanged, or
// removes it when the fresh object has none. This keeps its
// lastTransitionTime, which the cluster operator measures a stalled
// rollout from. A Lost phase is overwritten on purpose, because that
// is how a machine reports that it is back.
//
// before is the status that the object had when the pass began,
// marshaled to JSON. When the new status marshals to the same bytes,
// nothing is written, so a machine whose status does not change sends
// no writes to the API server and etcd. The heartbeat lease, not this
// write, shows that the machine is alive. Skipping against a stale
// copy is safe, because whatever changed the server's copy arrives on
// the watch and starts a pass that writes.
//
// A second conflict is not retried, because the write that won is
// already queued on the watch and triggers the pass that publishes.
func publishOwnStatus(r *reader, m *machine.Machine, status *machine.MachineStatus, before []byte) error {
	after, err := json.Marshal(status)
	if err == nil && bytes.Equal(before, after) {
		return nil
	}

	err = r.publishStatus(m, status)
	if !errors.Is(err, apiclient.ErrConflict) {
		return err
	}
	// freshMachine reads from the API server and not from the watch's
	// copy, because that copy can still lack the write that won.
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
