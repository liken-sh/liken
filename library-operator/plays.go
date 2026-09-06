package main

// The Play half of the progress flow. The progress pod holds no API
// credential, so it cannot read a Play's owner references or
// annotations. The operator reads them and publishes the audience on
// the bus, retained. The bus drops what nobody hears, so when a Play
// ends the operator also publishes its last status off the API server,
// which is where the position stands after the last report. The
// finalizer library.liken.sh/progress stays on the Play until the
// store says that last position is written, so a delete cannot
// outrun the record of it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// reconcileProgress is the progress half of one pass. The Plays go
// first, because a Person is released against what the stores answered
// about the Plays, and a Watch's status is the projection of the rows
// they wrote.
func (o *operator) reconcileProgress(ctx context.Context, plays []Play, people []Person,
	watches []Watch, stores map[string]bool, now time.Time) {
	o.reconcilePlays(ctx, plays, stores)
	o.reconcilePeople(ctx, people, stores, now)
	o.reconcileWatches(ctx, watches, people)
}

// storeNamespaces is the set of namespaces a progress store stands in.
// It is the rule the catalog pod stands on, one Catalog and no more,
// because a namespace with none and a namespace with two both stand no
// cluster and so record nothing.
func storeNamespaces(byNamespace map[string][]*NamespaceCatalog) map[string]bool {
	namespaces := map[string]bool{}
	for namespace, catalogs := range byNamespace {
		if singleCatalog(catalogs).catalog != nil {
			namespaces[namespace] = true
		}
	}
	return namespaces
}

// reconcilePlays holds, publishes, and releases every Play a store
// records, and releases the Plays no store will ever record. A failure
// on one Play is reported and the pass carries on, because one Play
// must not stop the record of the others.
func (o *operator) reconcilePlays(ctx context.Context, plays []Play, stores map[string]bool) {
	live := map[string]bool{}
	for index := range plays {
		play := &plays[index]
		// A namespace that stands no store records nothing, so a Play
		// there that carries the finalizer is one nobody will ever
		// release. The operator releases it itself.
		if !stores[play.Metadata.Namespace] {
			if play.Metadata.holds(progressFinalizer) {
				o.releasePlay(ctx, play)
			}
			continue
		}
		live[libraryKey(play.Metadata.Namespace, play.Metadata.Name)] = true
		o.reconcilePlay(ctx, play)
	}
	// A mark whose Play this pass did not see belongs to a Play that is
	// gone, and its retained messages are standing on the bus still.
	for _, key := range o.marks.retainRecorded(live) {
		namespace, name, _ := strings.Cut(key, "/")
		o.clearPlayTopics(namespace, name)
	}
}

// reconcilePlay runs one Play through the ladder: hold it, say who
// watched it, say how it ended, and release it once the store has the
// last position.
func (o *operator) reconcilePlay(ctx context.Context, play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name

	if !play.Metadata.deleting() && !play.Metadata.holds(progressFinalizer) {
		if _, err := PatchPlayMetadata(ctx, o.client, namespace, name,
			play.Metadata.ResourceVersion,
			ObjectMeta{Finalizers: play.Metadata.with(progressFinalizer)}); err != nil {
			fmt.Fprintf(os.Stderr, "holding play %s/%s: %v\n", namespace, name, err)
		}
	}

	o.publishMark(playAudienceTopic(o.topicBase, namespace, name), playAudienceOf(play))
	// A deleting Play reports no further position, whatever phase it
	// carries, so its status is final as it stands.
	if play.ended() {
		o.publishMark(playFinalTopic(o.topicBase, namespace, name), playFinalOf(play))
	}

	if !play.Metadata.deleting() {
		return
	}
	if recorded, held := o.marks.recordedFor(namespace, name); held && recorded.Ended {
		o.releasePlay(ctx, play)
	}
}

// playAudienceOf reads the audience off a Play: the Player it runs on,
// the Watch and the people its owner references name, and the aliases
// and numbers its annotations carry. A Play a person wrote by hand with
// the same references and annotations reads the same way.
func playAudienceOf(play *Play) playAudience {
	audience := playAudience{Library: play.Metadata.Annotations[libraryAnnotation]}
	if len(play.Spec.Players) > 0 {
		audience.Player = play.Spec.Players[0]
	}
	for _, owner := range play.Metadata.OwnerReferences {
		switch owner.Kind {
		case watchKind:
			audience.Watch = owner.Name
		case personKind:
			audience.People = append(audience.People, owner.Name)
		}
	}
	slices.Sort(audience.People)
	for key, id := range play.Metadata.Annotations {
		provider, aliased := strings.CutPrefix(key, aliasAnnotationPrefix)
		if !aliased {
			continue
		}
		if audience.Aliases == nil {
			audience.Aliases = map[string]string{}
		}
		audience.Aliases[provider] = id
	}
	audience.Season = numberAnnotation(play.Metadata.Annotations, seasonAnnotation)
	audience.Episode = numberAnnotation(play.Metadata.Annotations, episodeAnnotation)
	return audience
}

// numberAnnotation reads one number off an annotation, and 0 where the
// annotation is absent or holds anything else. A Play is a person's to
// write, so nothing here trusts the value.
func numberAnnotation(annotations map[string]string, key string) int {
	number, err := strconv.Atoi(annotations[key])
	if err != nil {
		return 0
	}
	return number
}

// playFinalOf is the Play's last status, as the store records it.
func playFinalOf(play *Play) playFinal {
	return playFinal{
		Phase:    play.Status.Phase,
		Item:     play.Status.Item,
		Position: play.Status.Position,
		Duration: play.Status.Duration,
	}
}

// releasePlay takes the finalizer off a Play the store has recorded, or
// one no store will record, then drops the three retained messages that
// stood for it. The finalizer goes first, because the object is what a
// person is waiting on and the topics are the operator's own to tidy.
func (o *operator) releasePlay(ctx context.Context, play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name

	_, err := PatchPlayMetadata(ctx, o.client, namespace, name, play.Metadata.ResourceVersion,
		ObjectMeta{Finalizers: play.Metadata.without(progressFinalizer)})
	if errors.Is(err, ErrConflict) {
		// A write between the list and this patch is another pass's,
		// and the next pass releases again.
		return
	}
	// An object that is already gone is the state this release was for.
	if err != nil && !errors.Is(err, ErrNotFound) {
		fmt.Fprintf(os.Stderr, "releasing play %s/%s: %v\n", namespace, name, err)
		return
	}
	o.clearPlayTopics(namespace, name)
	o.marks.dropRecorded(namespace, name)
}

// clearPlayTopics drops every retained message one Play stood on the
// bus: what the operator said about it, and what the store said back.
func (o *operator) clearPlayTopics(namespace, name string) {
	o.clearTopic(playAudienceTopic(o.topicBase, namespace, name))
	o.clearTopic(playFinalTopic(o.topicBase, namespace, name))
	o.clearTopic(playRecordedTopic(o.topicBase, namespace, name))
}
