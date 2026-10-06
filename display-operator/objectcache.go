package main

// The operator's passes read Displays, Layouts, and this node's pods
// from the watches' stores, not from the API server. Each store holds
// the same selection the passes read: every Display, every Layout, and
// the pods whose spec.nodeName is this node. So a settled pass sends
// the API server no read of these three kinds. The shared informer
// package holds the rules of that read: an object a store does not
// hold, and a store's copy older than this operator's own last write,
// are read from the API server, and a list comes from a store only
// after the store holds the whole first read.
//
// An object that a store does not hold is an object that does not
// exist yet, a pod on another node, or any object while its watch has
// not finished its first read.
//
// A Display's copy older than this operator's own write matters here
// because the Display controller acts on the hardware from what the
// status holds: a captured value, a mode the compositor declined, a
// write it already made. A pass that read an older copy would act again
// on a change it already made, such as restarting the compositor for a
// mode it already recorded as declined. So the Display store keeps a
// memo of the version of each Display's newest copy this operator wrote
// or read. The operator writes no Layout and no pod, so their stores
// need no memo.
//
// A write from a copy that another writer changed since carries an
// older resourceVersion, and the API server answers 409 Conflict.
// settleStatus then reads the Display from the API server and writes
// once more, if the fresh copy still needs the write; the placement
// reports and the sweep write through it, and each takes a 404 as a
// Display that is absent. writeStatus returns the conflict, and the
// Display controller's next pass reads the Display from the API server.

import (
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The three getters are the metadata the shared cache reads from a
// Display. A Display is cluster-scoped, so it has no namespace.
func (m *DisplayMeta) GetName() string            { return m.Name }
func (m *DisplayMeta) GetNamespace() string       { return "" }
func (m *DisplayMeta) GetResourceVersion() string { return m.ResourceVersion }

func (d *Display) GetObjectMeta() informer.Meta { return &d.Metadata }

// displayStore reads and writes the Displays for both passes, through
// the Display store and its memo. The Display controller and the
// placement pass run on their own goroutines and both write status,
// and the memo sends one request about each Display to the API server
// at a time (memo.Versions.Send). A read the store answers takes only the
// memo's lock, so it never waits on the other pass's request.
//
// The store posts the Events of each condition transition and each new
// unconfirmed write after the status write lands (announce), so a
// write the API server refuses posts nothing, and the next pass finds
// the same transition again. A nil recorder posts nothing.
type displayStore struct {
	client   *apiclient.Client
	held     informer.Held
	recorder *events.Recorder
}

// newDisplayStore builds the store over one watch's store view. The
// zero view holds nothing, and every Display is read from the API
// server.
func newDisplayStore(client *apiclient.Client, view informer.View) *displayStore {
	return &displayStore{client: client, held: informer.Held{View: view, Versions: memo.New()}}
}

func displayPath(name string) string { return DisplaysPath + "/" + name }

// get answers one Display: the store's copy when it holds a current
// one, and the API server's copy when it does not.
func (s *displayStore) get(name string) (*Display, error) {
	return informer.ReadOne[Display](s.client, s.held, name, displayPath(name))
}

// list answers every Display, from the store once it holds its first
// read, and from the API server before then (informer.List). A copy
// older than this process's own write or read is read again from the
// API server, and a Display the API server no longer holds is left out.
func (s *displayStore) list() ([]Display, error) {
	return informer.List[Display](s.client, s.held, DisplaysPath, displayPath)
}

// create creates one Display with an empty spec.
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

// writeStatus writes one Display's status from the copy it holds. A
// write the API server refuses with 409 Conflict came from a copy that
// another writer changed since, and the memo then holds no version any
// copy has, so the next read goes to the API server.
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

// settleStatus writes the status that compose makes from a Display's
// published status, when it differs. compose reports false when this
// node must leave the Display alone. A write refused because the copy
// is older than the API server's, or because the Display is gone,
// reads the Display again, composes again from the fresh copy, and
// writes once more. A Display that is gone answers
// apiclient.ErrNotFound.
func (s *displayStore) settleStatus(display *Display, compose func(published DisplayStatus) (DisplayStatus, bool)) error {
	var before DisplayStatus
	landed, err := informer.SettleStatus(s.client, s.held.Versions, displayPath(display.Metadata.Name), display, func(held *Display) bool {
		status, ours := compose(held.Status)
		if !ours || reflect.DeepEqual(held.Status, status) {
			return false
		}
		before = held.Status
		held.APIVersion, held.Kind, held.Status = DisplayAPIVersion, "Display", status
		return true
	})
	if landed {
		s.announce(display, before)
	}
	return err
}

// clusterStores is the Layout store and the store of this node's pods.
// The zero value holds nothing, and every read goes to the API server.
type clusterStores struct {
	layouts informer.View
	pods    informer.View
}

// layout answers one Layout. Once the store is ready, it holds every
// Layout, so a name it does not hold is a Layout that does not exist,
// and the read costs the API server nothing. The store can become
// ready, or stop, between two checks, so layout checks once and reads
// the store itself. A second check that disagreed with the first would
// answer ErrNotFound for a Layout the store holds. A copy that does not
// convert is read from the API server, as informer.ReadOne does.
func (c clusterStores) layout(client *apiclient.Client, name string) (*Layout, error) {
	if !c.layouts.Ready() {
		return getLayout(client, name)
	}
	object, held, err := c.layouts.Store.GetByKey(name)
	if err != nil || !held {
		return nil, apiclient.ErrNotFound
	}
	layout, err := informer.Convert[Layout](object)
	if err != nil {
		return getLayout(client, name)
	}
	return &layout, nil
}

// pod answers one pod, from the store when it holds it. The store
// holds this node's pods, so a pod on another node is read from the API
// server.
func (c clusterStores) pod(client *apiclient.Client, namespace, name string) (*Pod, error) {
	if held, ok := informer.Cached[Pod](c.pods, namespace+"/"+name); ok {
		return held, nil
	}
	return getPod(client, namespace, name)
}

// podsOn answers the pods on this node, from the store once it holds
// its first read.
func (c clusterStores) podsOn(client *apiclient.Client, node string) ([]Pod, error) {
	if c.pods.Ready() {
		return informer.CachedList[Pod](c.pods), nil
	}
	return listPods(client, node)
}
