package main

// The machine's Disruptions are this operator's own request for its
// staged documents and the requests that people file to reboot the
// machine.
//
// Every reboot and restart that this operator makes waits for a turn
// from cluster-operator (cluster-operator/rollout.go). cluster-operator
// grants a turn to a machine that has an approved Disruption that is
// not done, and records the turn in the Cluster's status.rebootTurns.
// While the machine waits, the operator reports the reason
// AwaitingTurn so that a person can see the wait. cluster-operator
// grants on the Disruption and not on the reason.
//
//   - The operator's own request, <machine>.machine-operator, covers
//     every staged document that waits for a reboot or a restart, and
//     a demotion. The operator updates the request when more is staged
//     and deletes it when nothing is. A deleted request takes its
//     approval with it, so the same bytes staged again need a new
//     approval.
//   - A person's request, <machine>.reboot-<boot>, asks for one reboot
//     of the boot that it names (rebootrequest.go). The operator
//     writes the running boot into a request that names no boot, and
//     marks a request Complete when the machine runs a different boot.

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

// requests returns the Disruptions for this machine. When the cluster
// does not serve the kind, or the operator's RBAC from an earlier
// release does not let it read them, it returns no requests and no
// error, so the operator asks for no turn.
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

// requestsPath is the path that lists one machine's Disruptions, using
// the field selector that the CRD declares.
func requestsPath(name string) string {
	return kubernetes.DisruptionsPath + "?fieldSelector=" + url.QueryEscape("spec.machine="+name)
}

func requestPath(name string) string { return kubernetes.DisruptionsPath + "/" + name }

// peopleRequests returns the requests that people filed, not the one
// this operator keeps, sorted by name.
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

// requestForBoot returns a person's request that is not complete and
// asks to reboot the running boot, or nil when there is none.
func requestForBoot(requests []disruption.Disruption, bootID string) *disruption.Disruption {
	for _, d := range peopleRequests(requests) {
		if d.Spec.Kind == disruption.KindReboot && !d.Complete() && machine.RebootRequestNames(d.Spec.Hash, bootID) {
			return &d
		}
	}
	return nil
}

// settlePeopleRequests updates each person's request to match the
// running boot. A request that names no boot gets the running boot,
// so a request written with kubectl alone still names the boot it
// asks to end. A request for an earlier boot is marked Complete. A
// request with no owner gets the Machine as its owner in the same
// write, so the garbage collector deletes it with the Machine, as it
// does a request that the CLI files.
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
			// A copy older than this operator's last write gets a
			// 409, and the next pass writes from a newer copy.
			if err := writeRequest(r.client, http.MethodPut, requestPath(d.Metadata.Name), &named); err != nil && !apiclient.Stale(err) {
				fmt.Printf("writing the boot hash and owner of Disruption %s: %v\n", d.Metadata.Name, err)
			}
		case d.Spent(bootID):
			complete := api.Condition{
				Type: disruption.CompleteCondition, Status: api.ConditionTrue, Reason: "BootChanged",
				ObservedGeneration: d.Metadata.Generation,
				Message: fmt.Sprintf("The request names boot %.12s, and the machine runs boot %.12s",
					d.Spec.Hash, bootID),
			}
			done := d
			done.Status.Conditions = api.SetCondition(slices.Clone(d.Status.Conditions), complete, now)
			if err := writeRequest(r.client, http.MethodPut, requestPath(d.Metadata.Name)+"/status", &done); err != nil {
				if !apiclient.Stale(err) {
					fmt.Printf("writing the Complete condition of Disruption %s: %v\n", d.Metadata.Name, err)
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

// ownRequest returns the spec of this operator's own request, or nil
// when nothing waits. The request covers each staged document in
// status.pending that waits for a disruption, and a demotion that
// waits for its reboot. A person's reboot request also appears in
// status.pending, but it has its own Disruption, so ownRequest leaves
// it out.
//
// The request's hash is over the condition and hash of each covered
// entry, so an approval of an earlier hash stops matching when the
// request covers something different.
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

// requestApprovalKey is the annotation that passes the approval of
// this operator's own request to the convergence decisions. It is set
// on the copy of the Machine that the decisions read, and its value is
// the comma-separated hashes of the staged documents that the approved
// request covers. The operator writes only the Machine's status
// subresource, so the annotation is never sent to the API server.
const requestApprovalKey = "machine-operator.liken.sh/request-approves"

// withRequestApprovals returns the copy of the Machine that the
// convergence decisions read. When a person approved the current hash
// of the operator's own request, requestApprovalKey lists the hashes
// of every staged document that the request covers.
//
// The approval counts only while the request covers what the last
// pass recorded in status.pending. A document staged since then
// changes the request's hash, so the approval no longer matches.
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

// approvedByRequest reports whether the approval from
// withRequestApprovals includes the staged document's hash.
func approvedByRequest(approval, hash string) bool {
	return hash != "" && slices.Contains(strings.Split(approval, ","), hash)
}

// syncOwnRequest creates, updates, or deletes this operator's request
// to match what waits. An update keeps spec.approvedHash, so an
// approval still applies when the hash is unchanged.
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
			fmt.Printf("deleted Disruption %s because the machine needs no disruption\n", name)
		}
	case at < 0:
		err = writeRequest(r.client, http.MethodPost, kubernetes.DisruptionsPath, disruption.New(name, m, *want))
		if err == nil {
			fmt.Printf("created Disruption %s: %s\n", name, want.Reason)
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
			fmt.Printf("updated Disruption %s: %s\n", name, want.Reason)
		}
	}
	switch {
	case err == nil, errors.Is(err, apiclient.ErrNotFound), errors.Is(err, apiclient.ErrForbidden):
	case errors.Is(err, apiclient.ErrConflict):
		// The copy was older than this operator's last write, or a
		// create raced another one. The next pass starts from the
		// current state.
	default:
		fmt.Printf("changing Disruption %s: %v\n", name, err)
	}
}

func writeRequest(c *apiclient.Client, method, path string, d *disruption.Disruption) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return c.RequestJSON(method, path, body, nil)
}

// requestsSource is the informer source for this machine's
// Disruptions. The CRD declares spec.machine as a selectable field, so
// the field selector keeps other machines' requests from reaching this
// pod.
func requestsSource(name string) informer.Source {
	return informer.Source{Resource: disruptionResource, FieldSelector: "spec.machine=" + name}
}
