package main

// One pass answers every Person whose status does not answer its spec,
// and follows every baker pod. The watches wake a pass (watch.go). A
// pass reads each Person from the watch's store, and from the API
// server when the store's copy is older than the operator's own last
// write, so a pass never reads a picture again because its own status
// write has not reached the watch yet.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

type operator struct {
	client *apiclient.Client
	people informer.Held
	bakers *bakers

	// recorder posts the Events (events.go). Nil posts none.
	recorder *events.Recorder

	// sources holds the schemes the operator reads in its own process
	// (source.go).
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

// pass answers every Person. recheck is the slow check, which reads
// every source whose picture can change with no edit to the Person. The
// pass answers the earliest deadline of a baker pod that still runs,
// and the zero time when none runs.
func (o *operator) pass(ctx context.Context, recheck bool) (time.Time, error) {
	people, err := informer.List[person](o.client, o.people, peoplePath, personPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("listing the people: %w", err)
	}
	pods, podsHeld := o.bakers.byPerson()
	var next time.Time
	var problems []error
	for index := range people {
		p := &people[index]
		deadline, err := o.answer(ctx, p, pods, podsHeld, recheck)
		if err != nil {
			problems = append(problems, fmt.Errorf("the Person %s: %w", p.Metadata.Name, err))
		}
		if !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
			next = deadline
		}
		delete(pods, p.Metadata.Name)
	}
	// A pod left in the map bakes for a Person that is gone.
	for _, baker := range pods {
		problems = append(problems, o.bakers.remove(baker))
	}
	return next, errors.Join(problems...)
}

// answer brings one Person's status up to date, and answers the
// deadline of its baker pod while one runs.
func (o *operator) answer(ctx context.Context, p *person, pods map[string]pod, podsHeld, recheck bool) (time.Time, error) {
	if baker, baking := pods[p.Metadata.Name]; baking {
		return o.followBaker(p, baker)
	}
	ref, err := url.Parse(p.Spec.Avatar)
	rechecking := recheck && err == nil && p.Spec.Avatar != "" && rechecked(ref)
	if !needsRead(p) && !rechecking {
		return time.Time{}, nil
	}

	out := outcome{source: p.Spec.Avatar, checked: p.checkRequest()}
	if p.Spec.Avatar == "" {
		out.initials = true
		return time.Time{}, o.settle(p, out)
	}
	if err != nil {
		out.err = &sourceError{reason: reasonUnsupportedScheme, err: fmt.Errorf("spec.avatar is not a URI: %w", err)}
		return time.Time{}, o.settle(p, out)
	}
	if read, inProcess := o.sources[ref.Scheme]; inProcess {
		out.reason = read.successReason()
		out.reading, out.err = read.read(ctx, ref, knownVersion(p), personColour(p.Metadata.Name))
		return time.Time{}, o.settle(p, out)
	}
	mount, baked, err := bakedScheme(ref, o.bakers.namespace)
	switch {
	case baked && err == nil && podsHeld:
		err := o.bakers.start(p, mount)
		// A claim in a namespace that does not exist answers 404 on
		// every try, so it is a failure of the source and not a
		// refusal to retry.
		if !errors.Is(err, apiclient.ErrNotFound) {
			return time.Time{}, err
		}
		out.err = failure(reasonBakeFailed, fmt.Errorf("the namespace %s does not exist", mount.namespace))
	case baked && err == nil:
		// The watch of the baker pods has not read them all yet, and
		// its first read wakes another pass.
		return time.Time{}, nil
	case baked:
		out.err = failure(reasonBakeFailed, err)
	default:
		out.err = &sourceError{reason: reasonUnsupportedScheme, err: fmt.Errorf(
			"the scheme %s: is not one the operator reads; it reads %s", ref.Scheme, supportedSchemes)}
	}
	return time.Time{}, o.settle(p, out)
}

// followBaker reads a baker pod's result, writes the status, and then
// deletes the pod.
func (o *operator) followBaker(p *person, baker pod) (time.Time, error) {
	out, deadline, err := o.bakers.follow(p, baker, time.Now())
	if out == nil || err != nil {
		return deadline, err
	}
	if err := o.settle(p, *out); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, o.bakers.remove(baker)
}

// settle writes the status that an outcome gives a Person, and writes
// nothing when the status already holds it. A Person deleted during
// the pass needs no status.
func (o *operator) settle(p *person, out outcome) error {
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
