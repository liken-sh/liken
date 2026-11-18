package main

// The passes read Displays, Layouts, and this node's pods from the
// watches' stores, so a pass that changes nothing sends the API server
// no read of these kinds. Each watch selects what the passes read:
// every Display, every Layout, and the pods whose spec.nodeName is
// this node. kubernetes/informer still reads from the API server in
// some cases, such as an object that the store does not have.
//
// The Display store also keeps a memo of the resourceVersion of each
// Display that this operator last wrote or read from the API server,
// and reads a Display again when the store's copy has another version.
// The Display controller decides what to do to the hardware from the
// status it reads, so a pass that read a copy from before its own
// write would repeat the change, for example restart the compositor
// for a mode that the status already records as declined. The operator
// writes no Layout and no pod, so those stores need no memo.

import (
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// A Display is cluster-scoped, so its namespace is empty.
func (m *DisplayMeta) GetName() string            { return m.Name }
func (m *DisplayMeta) GetNamespace() string       { return "" }
func (m *DisplayMeta) GetResourceVersion() string { return m.ResourceVersion }

func (d *Display) GetObjectMeta() informer.Meta { return &d.Metadata }

// displayStore reads and writes Displays for the Display controller and
// the placement pass, which run on separate goroutines and both write
// status. memo.Versions.Send sends one request per Display at a time,
// so the memo records the responses in the order the API server gave
// them.
//
// A status write posts its Events only after the API server accepts
// it, so a refused write leaves the transition for the next pass to
// post.
type displayStore struct {
	client   *apiclient.Client
	held     informer.Held
	recorder *events.Recorder
}

// With the zero view, every read goes to the API server.
func newDisplayStore(client *apiclient.Client, view informer.View) *displayStore {
	return &displayStore{client: client, held: informer.Held{View: view, Versions: memo.New()}}
}

func displayPath(name string) string { return DisplaysPath + "/" + name }

func (s *displayStore) get(name string) (*Display, error) {
	return informer.ReadOne[Display](s.client, s.held, name, displayPath(name))
}

// list leaves out a Display that the API server no longer has.
func (s *displayStore) list() ([]Display, error) {
	return informer.List[Display](s.client, s.held, DisplaysPath, displayPath)
}

func (s *displayStore) create(name string) (*Display, error) {
	var created *Display
	err := s.held.Versions.Send(name, func() (string, error) {
		var err error
		if created, err = createDisplay(s.client, name); err != nil {
			return "", err
		}
		return created.Metadata.ResourceVersion, nil
	})
	return created, err
}

// writeStatus replaces the status of the caller's copy of a Display. It
// returns a 409 Conflict to the caller and does not retry. After any
// failed write, the next read of that Display goes to the API server.
func (s *displayStore) writeStatus(display *Display, status DisplayStatus) error {
	written := *display
	written.APIVersion, written.Kind, written.Status = DisplayAPIVersion, "Display", status
	err := s.held.Versions.Send(display.Metadata.Name, func() (string, error) {
		if err := apiclient.ReplaceStatus(s.client, displayPath(display.Metadata.Name), &written); err != nil {
			return "", err
		}
		return written.Metadata.ResourceVersion, nil
	})
	if err == nil {
		before := display.Status
		*display = written
		s.announce(display, before)
	}
	return err
}

// settleStatus writes the status that compose builds from the Display's
// status, unless compose returns false, which means this node must
// leave the Display alone, or the status is unchanged. After a 409
// Conflict it reads the Display from the API server, composes again,
// and writes once more. When the Display no longer exists, it returns
// apiclient.ErrNotFound.
func (s *displayStore) settleStatus(display *Display, compose func(published DisplayStatus) (DisplayStatus, bool)) error {
	var before DisplayStatus
	landed, err := informer.SettleStatus(s.client, s.held.Versions, displayPath(display.Metadata.Name), display, func(current *Display) bool {
		status, ours := compose(current.Status)
		if !ours || reflect.DeepEqual(current.Status, status) {
			return false
		}
		before = current.Status
		current.APIVersion, current.Kind, current.Status = DisplayAPIVersion, "Display", status
		return true
	})
	if landed {
		s.announce(display, before)
	}
	return err
}

// With the zero clusterStores, every read goes to the API server.
type clusterStores struct {
	layouts informer.View
	pods    informer.View
}

// layout returns ErrNotFound for a name that a ready store lacks,
// without a request, because a ready store has every Layout and the
// operator creates none. It checks readiness once, because the store
// can become ready between two checks. A call to informer.Cached that
// found the store not ready, followed by a check that found it ready,
// would return ErrNotFound for a Layout that the store has.
func (c clusterStores) layout(client *apiclient.Client, name string) (*Layout, error) {
	if !c.layouts.Ready() {
		return getLayout(client, name)
	}
	object, exists, err := c.layouts.Store.GetByKey(name)
	if err != nil || !exists {
		return nil, apiclient.ErrNotFound
	}
	layout, err := informer.Convert[Layout](object)
	if err != nil {
		return getLayout(client, name)
	}
	return &layout, nil
}

// The store has only this node's pods, so a pod on another node comes
// from the API server.
func (c clusterStores) pod(client *apiclient.Client, namespace, name string) (*Pod, error) {
	if cached, ok := informer.Cached[Pod](c.pods, namespace+"/"+name); ok {
		return cached, nil
	}
	return getPod(client, namespace, name)
}

func (c clusterStores) podsOn(client *apiclient.Client, node string) ([]Pod, error) {
	if c.pods.Ready() {
		return informer.CachedList[Pod](c.pods), nil
	}
	return listPods(client, node)
}
