package main

// The Play half of the progress flow. The progress pod holds no API
// credential, so it cannot read a Play's owner references or
// annotations. The operator reads them and publishes the audience on
// the bus, retained. The bus drops what nobody hears, so when a Play
// ends the operator also publishes its last status from the API server,
// which records the position after the last report. The
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

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// storeNamespaces is the set of namespaces with a progress store.
// A namespace must have exactly one Catalog, because a namespace with
// none or with two cannot create a cluster and records nothing.
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
		// A namespace with no store records nothing, so a Play
		// there that carries the finalizer is one nobody will ever
		// release. The operator releases it itself.
		if !stores[play.Metadata.Namespace] {
			if play.Metadata.holds(progressFinalizer) {
				o.releasePlay(ctx, play, "namespace "+play.Metadata.Namespace+" holds no progress store")
			}
			continue
		}
		live[libraryKey(play.Metadata.Namespace, play.Metadata.Name)] = true
		o.reconcilePlay(ctx, play)
	}
	// A mark whose Play this pass did not see belongs to a Play that is
	// gone, and its retained messages are standing on the bus still.
	for _, key := range o.marks.goneRecorded(live) {
		namespace, name, _ := strings.Cut(key, "/")
		if err := o.clearPlayTopics(ctx, namespace, name); err != nil {
			fmt.Fprintf(os.Stderr, "clearing the topics of a Play that is gone: %v\n", err)
			return
		}
		o.marks.dropRecorded(namespace, name)
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
			fmt.Fprintf(os.Stderr, "holding the Play with uid %s: %s\n", play.Metadata.UID, playError(err, name))
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
		o.releasePlay(ctx, play, fmt.Sprintf("the progress store recorded its end at item %d, position %s",
			recorded.Item, recorded.Position))
	}
}

// playAudienceOf reads the audience off a Play: the Player it runs on,
// the people its owner references name, and the aliases and numbers
// its annotations carry. A Play a person wrote by hand with
// the same references and annotations reads the same way.
func playAudienceOf(play *Play) playAudience {
	audience := playAudience{Library: play.Metadata.Annotations[libraryAnnotation]}
	if len(play.Spec.Players) > 0 {
		audience.Player = play.Spec.Players[0]
	}
	for _, owner := range play.Metadata.OwnerReferences {
		if owner.Kind == personKind {
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
	audience.Credits = creditsSpans(play)
	return audience
}

// creditsSpans reads every credits mark of the Play's first item. The first
// item is the work the aliases and the numbers name, so its marks are the
// ones the watched rule reads. A Play that the browser creates carries the
// operator's one answer for the file's credits. Every credits mark travels,
// with both edges as the Play states them, because a Play from another
// client can carry several.
func creditsSpans(play *Play) []creditsSpan {
	if len(play.Spec.Items) == 0 || play.Spec.Items[0].Presentation == nil {
		return nil
	}
	var spans []creditsSpan
	for _, mark := range play.Spec.Items[0].Presentation.Marks {
		if mark.Kind == markKindCredits {
			spans = append(spans, creditsSpan{Start: mark.Start, End: mark.End})
		}
	}
	return spans
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
// stood for it. The finalizer goes first, because the store's recorded
// mark is what releases the Play, and a release that did not land after
// the clears would wait for a mark that the store does not publish
// again.
//
// The desk keeps the recorded mark when the bus did not take the clears.
// The next pass then finds a mark whose Play is gone and clears again
// (reconcilePlays), so a clear that the bus dropped is not lost.
//
// The line names the Play by its uid and its work, because its name is
// minted from the title.
func (o *operator) releasePlay(ctx context.Context, play *Play, why string) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name

	_, err := PatchPlayMetadata(ctx, o.client, namespace, name, play.Metadata.ResourceVersion,
		ObjectMeta{Finalizers: play.Metadata.without(progressFinalizer)})
	if errors.Is(err, apiclient.ErrConflict) {
		// A write after the copy this pass read is in the copy the
		// next pass reads, and that pass releases again.
		return
	}
	// An object that is already gone is the state this release was for.
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "releasing the Play with uid %s: %s\n", play.Metadata.UID, playError(err, name))
		return
	}
	audience := playAudienceOf(play)
	o.logf("released the Play with uid %s of %s, because %s", play.Metadata.UID,
		workNamed(audience.Aliases, audience.Season, audience.Episode), why)
	if err := o.clearPlayTopics(ctx, namespace, name); err != nil {
		fmt.Fprintf(os.Stderr, "clearing the topics of the Play with uid %s: %s\n", play.Metadata.UID, playError(err, name))
		return
	}
	o.marks.dropRecorded(namespace, name)
}

// clearPlayTopics drops every retained message one Play stood on the
// bus: what the operator said about it, and what the store said back.
// The store's recorded mark goes last, and the clears stop at the first
// one that the bus did not take. So while any topic of the Play stands,
// the recorded mark stands too, and the broker delivers it again at the
// operator's next subscribe, which leads a pass to clear again.
func (o *operator) clearPlayTopics(ctx context.Context, namespace, name string) error {
	return o.clearTopics(ctx,
		playAudienceTopic(o.topicBase, namespace, name),
		playFinalTopic(o.topicBase, namespace, name),
		playRecordedTopic(o.topicBase, namespace, name))
}

// playError is an API error about one Play as a line may carry it. The
// request in the error names the Play, and the API server minted that name
// from the title, so the name turns into its hash.
func playError(err error, name string) string {
	return strings.ReplaceAll(err.Error(), name, hashed(name))
}
