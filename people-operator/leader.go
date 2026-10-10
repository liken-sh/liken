package main

// The operator is a cluster singleton, because two copies that act at
// once race on the baker pods. Both copies follow the same finished
// baker pod. One copy reads its result, writes the status, and deletes
// the pod. The other copy then finds no log to read, and writes
// AvatarReady False with BakeFailed over a picture that baked. Two
// copies also fetch every picture twice and post every Event twice. A
// rolling update, a node partition, and a replica count above one each
// run a second copy, so the operator elects one acting copy with the
// Lease named people-operator, through kubernetes/election.
//
// Only the copy that holds the Lease watches, writes, and starts baker
// pods. Every other copy waits for the Lease and does nothing else. The
// client asks the election before each write (Lead.MayWrite), so a
// leader that cannot renew stops writing before another copy can take
// the Lease.

import (
	"context"
	"errors"
	"io"
	"net/http"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
)

// leaseName names the Lease the copies compete for, in the operator's
// own namespace.
const leaseName = "people-operator"

// process is what one process of the operator reaches: the API server,
// the Lease, and the picture servers.
type process struct {
	// client sends every request of the operator. It ends the wait after
	// a 429 on the stop, and does not cut off a request in flight, so a
	// write either returns before the release or was never sent.
	client *apiclient.Client
	// watcher is the client of the watches, which only read.
	watcher dynamic.Interface
	// lease is the configuration of the election's client.
	lease *rest.Config
	// transport reaches the https:// and http:// picture servers.
	transport http.RoundTripper

	// pod and namespace name the operator's own pod. The pod's name is
	// the start of the identity the Lease names, and the namespace holds
	// the Lease.
	pod       string
	namespace string

	// report receives each line the election logs, and eventLog each
	// line of the Event recorder. exit ends the process after a lost
	// Lease. A test sets all three.
	report   func(line string)
	eventLog io.Writer
	exit     func(code int)
}

// operate waits for the Lease, and then runs the operator until stop
// ends. A copy that never led answers nil when stop ends, because it
// holds nothing.
//
// The release comes after run returns. run returns only after the pass
// in flight has answered its Person, and every write of that pass
// returns before the next request is sent, so no write of the pass is
// in flight when the Lease is free. The Event recorder writes from its
// own goroutine, which stop ends. An Event write that stop cut off can
// still land after the release, and an Event is the one write that
// another copy does not read or overwrite.
func (p process) operate(stop context.Context) error {
	lead, err := election.Start(stop, p.lease, election.Options{
		Name:      leaseName,
		Namespace: p.namespace,
		Pod:       p.pod,
		Report:    p.report,
		Exit:      p.exit,
	})
	if errors.Is(err, election.ErrStopped) {
		return nil
	}
	if err != nil {
		return err
	}
	client := p.client.WithWriteGuard(lead.MayWrite)
	image, err := ownImage(client, p.namespace, p.pod)
	if err != nil {
		// The read wrote nothing, so the waiting copy may take the
		// Lease at once.
		lead.StepDown()
		return err
	}
	bakers := &bakers{client: client, image: image, namespace: p.namespace}
	recorder := events.New(stop, client, component, events.Options{Instance: p.pod, Log: p.eventLog})
	newOperator(client, p.transport, bakers, recorder).run(stop, p.watcher)
	lead.StepDown()
	return nil
}
