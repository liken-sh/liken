package main

// A pass updates every Person whose status is out of date with its
// spec, collects the result of each baker pod, and deletes baker pods
// whose Person is gone. A pass reads each Person from the informer's
// cache, or from the API server when the cache is older than the
// operator's last write to that Person. Without that fallback, a pass
// would read a picture again because its own status write has not
// reached the cache yet.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

type operator struct {
	client *apiclient.Client
	people informer.Held
	bakers *bakers

	// recorder posts the Events (events.go). When nil, none are posted.
	recorder *events.Recorder

	// sources maps each URI scheme that the operator reads in its own
	// process to its reader (source.go).
	sources map[string]source
}

func newOperator(client *apiclient.Client, transport http.RoundTripper, bakers *bakers, recorder *events.Recorder) *operator {
	fetch := newHTTPSource(transport)
	return &operator{
		client:   client,
		bakers:   bakers,
		recorder: recorder,
		sources: map[string]source{
			schemeHTTPS: fetch,
			schemeHTTP:  fetch,
			schemeData:  dataSource{},
		},
	}
}

// pass brings every Person up to date. When recheck is true, it also
// rereads every source whose picture can change without an edit to the
// Person. It returns the earliest deadline among the running baker
// pods, or the zero time when none is running.
func (o *operator) pass(ctx context.Context, recheck bool) (time.Time, error) {
	people, err := informer.List[person](o.client, o.people, peoplePath, personPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("listing the people: %w", err)
	}
	pods, replaced, podsListed := o.bakers.byPerson()
	var next time.Time
	var problems []error
	for index := range people {
		// On shutdown, stop between Persons. The next operator process
		// does the rest.
		if ctx.Err() != nil {
			return next, errors.Join(problems...)
		}
		p := &people[index]
		deadline, err := o.reconcile(ctx, p, pods, podsListed, recheck)
		if err != nil {
			problems = append(problems, fmt.Errorf("reconciling the Person %s: %w", p.Metadata.Name, err))
		}
		if !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
			next = deadline
		}
		delete(pods, p.Metadata.Name)
	}
	// A pod still in the map belongs to a Person that no longer exists.
	for _, baker := range append(replaced, slices.Collect(maps.Values(pods))...) {
		problems = append(problems, o.bakers.remove(baker))
	}
	return next, errors.Join(problems...)
}

// reconcile brings one Person's status up to date. While a baker pod
// runs for the Person, it returns that pod's deadline.
func (o *operator) reconcile(ctx context.Context, p *person, pods map[string]pod, podsListed, recheck bool) (time.Time, error) {
	if baker, baking := pods[p.Metadata.Name]; baking {
		return o.writeBakerResult(p, baker)
	}
	ref, err := url.Parse(p.Spec.Avatar)
	rechecking := recheck && err == nil && p.Spec.Avatar != "" && rechecked(ref)
	if !needsRead(p) && !rechecking {
		return time.Time{}, nil
	}

	out := outcome{source: p.Spec.Avatar, checked: p.checkRequest()}
	if p.Spec.Avatar == "" {
		out.initials = true
		return time.Time{}, o.writeOutcome(p, out)
	}
	if err != nil {
		out.err = &sourceError{reason: reasonUnsupportedScheme, err: fmt.Errorf("spec.avatar is not a URI: %w", err)}
		return time.Time{}, o.writeOutcome(p, out)
	}
	if read, inProcess := o.sources[ref.Scheme]; inProcess {
		out.reason = read.successReason()
		out.reading, out.err = read.read(ctx, ref, knownVersion(p), personColour(p.Metadata.Name))
		// A shutdown that cancelled the read says nothing about the
		// source, so do not write FetchFailed. The next operator
		// process reads again.
		if ctx.Err() != nil {
			return time.Time{}, nil
		}
		return time.Time{}, o.writeOutcome(p, out)
	}
	mount, baked, err := bakerMountFor(ref, o.bakers.namespace)
	switch {
	case baked && err == nil && podsListed:
		_, err := o.bakers.start(p, mount, 0)
		// A claim URI whose namespace does not exist gets 404 on every
		// try. That is a failed source for the condition, and retrying
		// would not help.
		if !errors.Is(err, apiclient.ErrNotFound) {
			return time.Time{}, err
		}
		out.err = failure(reasonBakeFailed, fmt.Errorf("the namespace %s in the claim:// URI does not exist", mount.namespace))
	case baked && err == nil:
		// The baker pod informer has not finished its first list, so
		// the pods are not known yet. Its sync starts another pass.
		return time.Time{}, nil
	case baked:
		out.err = failure(reasonBakeFailed, err)
	default:
		out.err = &sourceError{reason: reasonUnsupportedScheme, err: fmt.Errorf(
			"spec.avatar has the scheme %q, and the operator reads only %s", ref.Scheme, supportedSchemes)}
	}
	return time.Time{}, o.writeOutcome(p, out)
}

// writeBakerResult writes the status from a baker pod's result and then
// deletes the pod. While the pod has no result, it returns the pod's
// deadline.
func (o *operator) writeBakerResult(p *person, baker pod) (time.Time, error) {
	// A pod that the kubelet ended says nothing about the picture, so
	// it writes no outcome, and AvatarReady keeps its value. The ended
	// pod stays until its wait is over, so a person can read its status,
	// and the pass wakes at the end of the wait.
	live := baker.Metadata.DeletionTimestamp == "" && baker.Metadata.Annotations[bakerSourceKey] == p.Spec.Avatar
	if live && baker.endedByKubelet() {
		// A new check-avatar value asks for a new bake, and the person
		// may have changed the Person to fix what the kubelet refused.
		// So the ended pod goes at once, and the pass that its delete
		// event starts finds that needsRead holds and starts the first
		// pod of the new bake, with no wait and no count. A new
		// spec.avatar takes the same path through inspect.
		if baker.Metadata.Annotations[bakerCheckedKey] != p.checkRequest() {
			return time.Time{}, o.bakers.remove(baker)
		}
		if at := baker.retryAt(); time.Now().Before(at) {
			return at, nil
		}
		created, err := o.bakers.replace(p, baker)
		if err != nil {
			return time.Time{}, err
		}
		// A retry loop posts its first failure and not each retry. A
		// pod that ran for longer than maxRetryWait starts a new loop.
		if created && (baker.tries() == 0 || baker.ran() > maxRetryWait) {
			o.postRecreated(p, baker)
		}
		return time.Time{}, nil
	}
	out, deadline, err := o.bakers.inspect(p, baker, time.Now())
	if out == nil || err != nil {
		return deadline, err
	}
	if err := o.writeOutcome(p, *out); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, o.bakers.remove(baker)
}

// writeOutcome writes the status that an outcome gives a Person, when
// that differs from the current status. A Person deleted during the
// pass is not an error.
func (o *operator) writeOutcome(p *person, out outcome) error {
	now := time.Now()
	var changed change
	written, err := informer.SettleStatus[person](o.client, o.people.Versions, personPath(p.Metadata.Name), p, func(held *person) bool {
		var next personStatus
		next, changed = composeStatus(held, out, now)
		if !statusChanged(held.Status, next) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = personAPIVersion, personKind, next
		return true
	})
	if written {
		o.post(p, changed)
	}
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
