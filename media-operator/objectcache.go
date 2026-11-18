package main

// A pass reads Plays and Players from the stores of their watches
// (clusterview.go), so a pass on a settled cluster sends the API server
// no read of either kind.
//
// A store's copy can be older than this operator's last write, because
// the watch delivers the write after the API server has responded to
// it, and later still while the watch is down. A pass acts on the status
// of both kinds, which this operator writes, so an old copy causes
// faults a person can see:
//
//   - The pass publishes each Player's activity, from its Play's phase, on
//     the bus before it reads anything else. From an old copy it would
//     publish the phase the Play already left, and a screen would show
//     Playing, Starting, Playing.
//   - A Player's status records the Player's screen and Sinks, and the
//     pass keeps them when no claim has newer information. From an old
//     copy it would write back the older record, and the Player would lose
//     the screen or Sinks it had found.
//
// So the memo of each kind (kubernetes/memo) records the resourceVersion
// of the newest copy of each object that this operator wrote or read,
// and a read goes to the API server while the store's copy has a
// different version. When the watch delivers the write, the versions
// match and the store serves the read again.
//
// A status write from a copy that someone else changed has an old
// resourceVersion, and the API server returns 409 Conflict.
// informer.SettleStatus then reads the object again and writes once
// more if the fresh copy still needs the write. Only this operator
// writes the status of these kinds, so the conflict comes from a change
// to the spec or the metadata, and the status written on the retry can
// still reflect the older spec. The watch event of that spec change
// starts the next pass, which writes the status of the new spec.
//
// Remotes are read through clusterview.go with no memo, because the pass
// composes a Remote's whole status from its claim and the Peripherals,
// and reads nothing back from it.

import (
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The informer and memo packages read the name, namespace, and
// resourceVersion of Plays and Players through these methods.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (p *Play) GetObjectMeta() informer.Meta   { return &p.Metadata }
func (p *Player) GetObjectMeta() informer.Meta { return &p.Metadata }

// currentKind lists every object of one kind through
// informer.CurrentList, and drops the memo's record of each object that
// no longer exists. Plays are created and deleted often, so without this
// the memo would grow for the life of the process.
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

func playKey(play *Play) string { return informer.Key(&play.Metadata) }

// Plays returns every Play, from the store when its copy is current and
// from the API server otherwise. A Play that the API server returns 404
// for is left out.
func (v *clusterView) Plays(c *apiclient.Client) ([]Play, error) {
	return currentKind[Play](c, v.plays, playPath)
}

// Players returns every Player, the same way as Plays.
func (v *clusterView) Players(c *apiclient.Client) ([]Player, error) {
	return currentKind[Player](c, v.players, playerPath)
}

// Player returns one Player, from the store when its copy is current and
// from the API server otherwise.
func (v *clusterView) Player(c *apiclient.Client, namespace, name string) (*Player, error) {
	return informer.ReadOne[Player](c, v.players, namespacedKey(namespace, name), playerPath(namespace, name))
}

// FreshPlayer reads one Player from the API server and records its
// version in the memo.
func (v *clusterView) FreshPlayer(c *apiclient.Client, namespace, name string) (*Player, error) {
	return informer.ReadFresh[Player](c, v.players.Versions, namespacedKey(namespace, name), playerPath(namespace, name))
}
