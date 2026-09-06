package main

// The Watch half of the progress flow. The operator puts an owner
// reference on each Watch for every Person it names, so the garbage
// collector removes the Watch when the last of its people is deleted.
// It writes the projection the store publishes into the Watch's
// status. Nothing here reads the catalog: what comes next in a series
// is a question the browser answers from its own copy of both files.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
)

// reconcileWatches ties every Watch to its people and writes back what
// the store published for it. A failure on one Watch is reported and
// the pass carries on.
func (o *operator) reconcileWatches(ctx context.Context, watches []Watch, people []Person) {
	for index := range watches {
		o.reconcileWatch(ctx, &watches[index], people)
	}
}

func (o *operator) reconcileWatch(ctx context.Context, watch *Watch, people []Person) {
	namespace, name := watch.Metadata.Namespace, watch.Metadata.Name

	if owners := watchOwners(watch, people); !slices.Equal(owners, watch.Metadata.OwnerReferences) {
		if _, err := PatchWatchOwnerReferences(ctx, o.client, namespace, name,
			watch.Metadata.ResourceVersion, owners); err != nil {
			fmt.Fprintf(os.Stderr, "owning watch %s/%s: %v\n", namespace, name, err)
			return
		}
	}

	progress, held := o.marks.progressFor(namespace, name)
	if !held {
		return
	}
	status := watchStatusOf(progress)
	if status == watch.Status {
		return
	}
	written := *watch
	written.Status = status
	if _, err := UpdateWatchStatus(ctx, o.client, &written); err != nil {
		fmt.Fprintf(os.Stderr, "writing the status of watch %s/%s: %v\n", namespace, name, err)
	}
}

// watchOwners is the owner list a Watch carries: one Person per name in
// its spec that the cluster holds, in name order, beside any owner of
// another kind that something else put there. A name that names nobody
// is dropped, because a reference to an object that does not exist
// would have the garbage collector delete the Watch.
func watchOwners(watch *Watch, people []Person) []OwnerReference {
	owners := []OwnerReference{}
	for _, owner := range watch.Metadata.OwnerReferences {
		if owner.Kind != personKind {
			owners = append(owners, owner)
		}
	}
	named := []OwnerReference{}
	for _, name := range watch.Spec.People {
		if person := personNamed(people, name); person != nil {
			named = append(named, personOwner(person))
		}
	}
	sort.Slice(named, func(one, other int) bool { return named[one].Name < named[other].Name })
	return append(owners, named...)
}

// watchStatusOf is the projection as the status carries it. The store
// publishes one observation of the whole Watch, so the operator writes
// the whole block and never a field of it.
func watchStatusOf(progress watchProgress) WatchStatus {
	return WatchStatus{
		Play:         progress.Play,
		Item:         progress.Item,
		Position:     progress.Position,
		Duration:     progress.Duration,
		Season:       progress.Season,
		Episode:      progress.Episode,
		Ended:        progress.Ended,
		LastRecorded: progress.LastRecorded,
	}
}

// watchNamed is the Watch one namespace holds under a name, or nothing.
// A play request names a Watch in the Player's own namespace, because
// an owner reference never crosses one.
func watchNamed(watches []Watch, namespace, name string) *Watch {
	for index := range watches {
		watch := &watches[index]
		if watch.Metadata.Namespace == namespace && watch.Metadata.Name == name {
			return watch
		}
	}
	return nil
}

// watchOwner is one Watch as an owner reference on a Play. There is no
// controller flag, for the reason a Person's carries none.
func watchOwner(watch *Watch) OwnerReference {
	return OwnerReference{
		APIVersion: libraryAPIVersion,
		Kind:       watchKind,
		Name:       watch.Metadata.Name,
		UID:        watch.Metadata.UID,
	}
}
