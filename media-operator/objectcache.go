package main

// A pass reads the Plays and the Players from their watches' stores,
// through the shared informer package. The view (clusterview.go) holds
// the stores, and each store holds its whole kind, so a settled pass
// sends the API server no read of a Play or a Player. The view exists
// only after every watch has read its collection once. While the API
// server forbids a watch, its store is not ready, and the shared reads
// read from the API server each Play or Player that the store holds.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. The operator
// writes the status of both kinds, and a pass acts on what that status
// holds:
//
//   - The pass derives a unit's activity from each Play's phase, and
//     publishes it on the bus before it reads anything else. A pass that
//     read the copy its own write replaced would publish the phase the
//     Play already left, and then, when the reconcile wrote the phase
//     again, the phase it had moved to: Playing, Starting, Playing on a
//     subscriber's screen.
//   - A Player's status remembers the unit's screen and its Sinks, which
//     the pass carries forward when no claim says better. A pass that
//     read the copy its own write replaced would carry forward the older
//     memory and write it back, and the unit would forget the screen or
//     the Sinks it learned.
//
// So the memo of each kind records the version of the newest copy this
// operator wrote or read, and the shared reads read an object from the
// API server when the store's copy has another version. Once the watch
// delivers the write, the versions match and the store answers again.
//
// A status write from a copy that another writer changed since carries
// an older resourceVersion, and the API server answers 409 Conflict.
// informer.SettleStatus then reads the object from the API server, and
// writes once more if the fresh copy still needs the write. Only this
// operator writes the status of either kind, and the memo keeps each
// pass on a copy at least as new as the operator's last write, so the
// status on the fresh copy is the one the pass composed from. The
// conflict comes from a change to the spec or the metadata. The status
// the retry writes can still carry what the pass derived from the older
// spec, such as a Player's idle controller. The spec edit's own watch
// event wakes the next pass, and that pass writes the status the new
// spec derives.
//
// The Remotes' store answers through clusterview.go with no memo. The
// pass reads no memory back from a Remote's status: it composes the
// whole status from the claim and the Peripherals each pass.

import (
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The three getters are the metadata the shared cache reads from each
// of this operator's kinds.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (p *Play) GetObjectMeta() informer.Meta   { return &p.Metadata }
func (p *Player) GetObjectMeta() informer.Meta { return &p.Metadata }

// currentKind answers every object of one kind through
// informer.CurrentList, and forgets the memo's record of each object
// that is gone. Plays come and go with every film, and a Player can be
// deleted, so without that the memo would keep a record of every object
// for the life of the process.
func currentKind[T any, P informer.Object[T]](c *apiclient.Client, held informer.Held, path func(namespace, name string) string) ([]T, error) {
	objects, err := informer.CurrentList[T, P](c, held, memo.NamespacedPath(path))
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(objects))
	for index := range objects {
		listed[informer.Key(P(&objects[index]).GetObjectMeta())] = true
	}
	held.Versions.ForgetGone(held.View.Store, listed)
	return objects, nil
}

// playKey is the store key of one Play.
func playKey(play *Play) string { return informer.Key(&play.Metadata) }

// Plays answers every Play: the store's copy of each Play when it holds
// a current one, and the API server's copy when it does not. A Play the
// API server no longer holds is left out.
func (v *clusterView) Plays(c *apiclient.Client) ([]Play, error) {
	return currentKind[Play](c, v.plays, playPath)
}

// Players answers every Player the same way.
func (v *clusterView) Players(c *apiclient.Client) ([]Player, error) {
	return currentKind[Player](c, v.players, playerPath)
}

// Player answers one Player: the store's copy when it holds a current
// one, and the API server's copy when it does not.
func (v *clusterView) Player(c *apiclient.Client, namespace, name string) (*Player, error) {
	return informer.ReadOne[Player](c, v.players, namespacedKey(namespace, name), playerPath(namespace, name))
}

// FreshPlayer reads one Player from the API server, and notes its
// version.
func (v *clusterView) FreshPlayer(c *apiclient.Client, namespace, name string) (*Player, error) {
	return informer.ReadFresh[Player](c, v.players.Versions, namespacedKey(namespace, name), playerPath(namespace, name))
}
