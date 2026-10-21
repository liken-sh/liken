package main

// The machine's Disruptions: the request this operator keeps for its
// own staged documents, and the reboots that people ask for.
//
// Every reboot and restart this operator makes waits for a turn that
// the rollout conductor grants (cluster-operator/rollout.go), and the
// conductor grants a turn to a machine with an approved Disruption
// that is not done. The turn is the machine's entry in the Cluster's
// ledger (turn.go). The operator still reports the reason AwaitingTurn,
// which a conductor of the release before grants on.
//
//   - The operator keeps one request, <machine>.machine-operator, for
//     every staged document that waits for a reboot or a restart, and
//     for a demotion. It updates the request when more is staged, and
//     deletes it when nothing is. A deleted request takes its approval
//     with it, so a later return to the same bytes asks again.
//   - A person's request, <machine>.reboot-<boot>, asks for one reboot
//     of the boot it names (rebootrequest.go). The operator writes the
//     running boot into a request that names none, and marks a request
//     Complete when the machine runs another boot.
//
// A cluster that does not serve the kind, or an RBAC from the release
// before it, reads as no requests at all, and the operator asks for no
// turn.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
)

// requests reads the Disruptions that name this machine. A kind the
// cluster does not serve or does not let this operator read answers
// none, with no error.
func (r *reader) requests(name string) ([]disruption.Disruption, error) {
	view := r.view(r.disruptions)
	if view.Ready() && view.Absent {
		return nil, nil
	}
	if held, ok := watch.List[disruption.Disruption](view); ok {
		return held, nil
	}
	listed, err := kubernetes.List[disruption.Disruption](r.client, requestsPath(name))
	if errors.Is(err, apiclient.ErrNotFound) || errors.Is(err, apiclient.ErrForbidden) {
		return nil, nil
	}
	return listed, err
}

// requestsPath lists the Disruptions of one machine, with the field
// selector the CRD declares.
func requestsPath(name string) string {
	return kubernetes.DisruptionsPath + "?fieldSelector=" + url.QueryEscape("spec.machine="+name)
}

func requestPath(name string) string { return kubernetes.DisruptionsPath + "/" + name }

// peopleRequests answers the requests that a person filed, not this
// operator, in name order.
func peopleRequests(requests []disruption.Disruption) []disruption.Disruption {
	var people []disruption.Disruption
	for _, d := range requests {
		if !d.Own() {
			people = append(people, d)
		}
	}
	slices.SortFunc(people, func(a, b disruption.Disruption) int { return strings.Compare(a.Metadata.Name, b.Metadata.Name) })
	return people
}

// requestForBoot answers a person's request that asks to reboot the
// running boot and is not complete, or nil.
func requestForBoot(requests []disruption.Disruption, bootID string) *disruption.Disruption {
	for _, d := range peopleRequests(requests) {
		if d.Spec.Kind == disruption.KindReboot && !d.Complete() && machine.RebootRequestNames(d.Spec.Hash, bootID) {
			return &d
		}
	}
	return nil
}

// settlePeopleRequests keeps each person's request current with the
// boot: a request that names no boot gets the running one, so a request
// written with kubectl alone names the boot it asks to end, and a
// request for an earlier boot is marked Complete. A request with no
// owner gets the Machine as its owner in the same write, so the
// garbage collector deletes it with the Machine, the same as a request
// the CLI files.
func settlePeopleRequests(r *reader, m *machine.Machine, requests []disruption.Disruption, bootID string, now time.Time) {
	if bootID == "" {
		return
	}
	for _, d := range peopleRequests(requests) {
		switch {
		case d.Complete():
		case d.Spec.Hash == "" || len(d.Metadata.OwnerReferences) == 0:
			named := d
			if named.Spec.Hash == "" {
				named.Spec.Hash = bootID
			}
			if len(named.Metadata.OwnerReferences) == 0 && m.Metadata.UID != "" {
				named.Metadata.OwnerReferences = disruption.New(d.Metadata.Name, m, d.Spec).Metadata.OwnerReferences
			}
			// A copy older than this operator's last write gets a 409,
			// and the next pass writes from a fresh one.
			if err := writeRequest(r.client, http.MethodPut, requestPath(d.Metadata.Name), &named); err != nil && !apiclient.Stale(err) {
				fmt.Printf("naming the boot and the owner in the Disruption %s: %v\n", d.Metadata.Name, err)
			}
		case d.Spent(bootID):
			complete := api.Condition{
				Type: disruption.CompleteCondition, Status: api.ConditionTrue, Reason: "BootChanged",
				ObservedGeneration: d.Metadata.Generation,
				Message: fmt.Sprintf("the request names boot %.12s, and the machine runs boot %.12s",
					d.Spec.Hash, bootID),
			}
			done := d
			done.Status.Conditions = api.SetCondition(slices.Clone(d.Status.Conditions), complete, now)
			if err := writeRequest(r.client, http.MethodPut, requestPath(d.Metadata.Name)+"/status", &done); err != nil {
				if !apiclient.Stale(err) {
					fmt.Printf("completing the Disruption %s: %v\n", d.Metadata.Name, err)
				}
				continue
			}
			r.recorder.Transition(requestReference(&d), complete, "")
		}
	}
}

func requestReference(d *disruption.Disruption) events.ObjectReference {
	return events.ObjectReference{APIVersion: api.APIVersion, Kind: "Disruption", Name: d.Metadata.Name, UID: d.Metadata.UID}
}

// ownRequest answers the spec of this operator's own request: one
// entry for each staged document in status.pending that waits for a
// disruption, and one for a demotion that waits for its reboot. A
// person's reboot request is in status.pending too, and it has its own
// Disruption, so it is left out. It answers nil when nothing waits.
//
// The hash covers the condition and the hash of each entry, so the
// request's hash changes whenever what it applies changes, and an
// approval of the old hash no longer matches.
func ownRequest(name string, pending []machine.PendingDisruption, demotion api.Condition) *disruption.Spec {
	var lines, reasons []string
	kind := disruption.KindRestart
	for _, p := range pending {
		if p.Condition == rebootRequestCondition {
			continue
		}
		lines = append(lines, p.Condition+"="+p.Hash)
		reasons = append(reasons, p.Summary)
		if p.Kind == machine.DisruptionReboot {
			kind = disruption.KindReboot
		}
	}
	switch demotion.Reason {
	case "DemotionPending", "AwaitingTurn", "DemotionRebooting":
		lines = append(lines, demotion.Type+"=demotion")
		reasons = append(reasons, "the demotion of this machine to follower")
		kind = disruption.KindReboot
	}
	if len(lines) == 0 {
		return nil
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return &disruption.Spec{
		Machine:   name,
		Requester: disruption.MachineOperator,
		Kind:      kind,
		Reason:    strings.Join(reasons, "; "),
		Hash:      hex.EncodeToString(sum[:]),
	}
}

// requestApprovalKey carries the approval of this operator's own
// request to the convergence decisions, on the decisions' own copy of
// the Machine: the hashes of the staged documents that the approved
// request covers, separated by commas. The operator writes only the
// Machine's status subresource, so the key never reaches the API
// server, and nothing outside this operator reads it.
const requestApprovalKey = "machine-operator.liken.sh/request-approves"

// withRequestApprovals answers the copy of the Machine that the
// convergence decisions read. A request whose spec.approvedHash names
// its hash approves every staged document it covers, so one approval
// releases one Disruption, whatever it applies.
//
// The approval counts only while the request covers what this
// operator found waiting on its last pass, which status.pending holds.
// A document staged since then changes the request's hash on this
// pass, and the approval no longer matches. A request deleted when
// nothing waited took its approval with it, so a later return to the
// same bytes asks again.
func withRequestApprovals(m *machine.Machine, requests []disruption.Disruption) *machine.Machine {
	decided := *m
	decided.Metadata.Annotations = maps.Clone(m.Metadata.Annotations)
	delete(decided.Metadata.Annotations, requestApprovalKey)
	at := slices.IndexFunc(requests, func(d disruption.Disruption) bool { return d.Own() })
	if at < 0 || !requests[at].ApprovedByHash() {
		return &decided
	}
	demotion := api.Condition{}
	if c := api.FindCondition(m.Status.Conditions, "NodeCurrent"); c != nil {
		demotion = *c
	}
	covered := ownRequest(m.Metadata.Name, m.Status.Pending, demotion)
	if covered == nil || covered.Hash != requests[at].Spec.Hash {
		return &decided
	}
	var hashes []string
	for _, p := range m.Status.Pending {
		if p.Condition != rebootRequestCondition {
			hashes = append(hashes, p.Hash)
		}
	}
	if decided.Metadata.Annotations == nil {
		decided.Metadata.Annotations = map[string]string{}
	}
	decided.Metadata.Annotations[requestApprovalKey] = strings.Join(hashes, ",")
	return &decided
}

// approvedByRequest reports whether a decision's approval names the
// staged document's hash.
func approvedByRequest(approval, hash string) bool {
	return hash != "" && slices.Contains(strings.Split(approval, ","), hash)
}

// syncOwnRequest makes this operator's request match what waits:
// it creates the request, updates its kind, reason, and hash, or
// deletes it when nothing waits. An update keeps spec.approvedHash, so
// a person's approval of the current hash stands and an approval of an
// earlier one no longer matches.
func syncOwnRequest(r *reader, m *machine.Machine, requests []disruption.Disruption, want *disruption.Spec) {
	name := disruption.OwnName(m.Metadata.Name)
	at := slices.IndexFunc(requests, func(d disruption.Disruption) bool { return d.Metadata.Name == name })
	var err error
	switch {
	case want == nil && at < 0:
		return
	case want == nil:
		err = r.client.RequestJSON(http.MethodDelete, requestPath(name), nil, nil)
		if err == nil {
			fmt.Printf("deleted the Disruption %s; nothing waits for a disruption\n", name)
		}
	case at < 0:
		err = writeRequest(r.client, http.MethodPost, kubernetes.DisruptionsPath, disruption.New(name, m, *want))
		if err == nil {
			fmt.Printf("created the Disruption %s: %s\n", name, want.Reason)
		}
	default:
		held := requests[at]
		next := *want
		next.ApprovedHash = held.Spec.ApprovedHash
		if held.Spec == next {
			return
		}
		updated := held
		updated.Spec = next
		err = writeRequest(r.client, http.MethodPut, requestPath(name), &updated)
		if err == nil {
			fmt.Printf("updated the Disruption %s: %s\n", name, want.Reason)
		}
	}
	switch {
	case err == nil, errors.Is(err, apiclient.ErrNotFound), errors.Is(err, apiclient.ErrForbidden):
	case errors.Is(err, apiclient.ErrConflict):
		// A copy older than this operator's last write, or a create
		// that crossed one, settles on the next pass.
	default:
		fmt.Printf("keeping the Disruption %s: %v\n", name, err)
	}
}

func writeRequest(c *apiclient.Client, method, path string, d *disruption.Disruption) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return c.RequestJSON(method, path, body, nil)
}

// requestsSource is the watch of this machine's Disruptions. Each one
// names its machine in spec.machine, which the CRD declares as a
// selectable field, so no other machine's request reaches this pod.
func requestsSource(name string) informer.Source {
	return informer.Source{Resource: disruptionResource, FieldSelector: "spec.machine=" + name}
}
