package main

// Deleting a Person deletes their progress. The operator asks every
// namespace's store to drop that person's rows, and holds
// library.liken.sh/progress on the Person until every store answers.
// A plays row with no people left stays as the Player's own row, and a
// shared Watch keeps the people who remain. The garbage collector
// removes the Plays and the Watches that named nobody else.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"
)

// personForgetRequest is the operator's ask that one person's rows go,
// published retained on that person's forget topic. The time is when
// the operator asked, so a store that starts later reads an ask it has
// not answered.
type personForgetRequest struct {
	At string `json:"at"`
}

// reconcilePeople holds every Person and releases the ones every store
// has answered for. A failure on one Person is reported and the pass
// carries on.
func (o *operator) reconcilePeople(ctx context.Context, people []Person, stores map[string]bool, now time.Time) {
	for index := range people {
		person := &people[index]
		if !person.Metadata.deleting() {
			o.holdPerson(ctx, person)
			continue
		}
		// A Person that does not hold this operator's finalizer is the
		// API server's to remove.
		if person.Metadata.holds(progressFinalizer) {
			o.forgetPerson(ctx, person, stores, now)
		}
	}
}

// holdPerson puts the finalizer on, so a delete cannot outrun the sweep
// of that person's rows.
func (o *operator) holdPerson(ctx context.Context, person *Person) {
	if person.Metadata.holds(progressFinalizer) {
		return
	}
	if _, err := PatchPersonFinalizers(ctx, o.client, person.Metadata.Name,
		person.Metadata.ResourceVersion, person.Metadata.with(progressFinalizer)); err != nil {
		fmt.Fprintf(os.Stderr, "holding person %s: %v\n", person.Metadata.Name, err)
	}
}

// forgetPerson asks every store to drop the person's rows and releases
// the Person once every namespace that stands one has answered. A
// cluster with no store anywhere releases at once, because there is
// nothing to sweep.
func (o *operator) forgetPerson(ctx context.Context, person *Person, stores map[string]bool, now time.Time) {
	name := person.Metadata.Name
	forget := personForgetTopic(o.topicBase, name)
	o.publishStanding(forget, personForgetRequest{At: now.UTC().Format(time.RFC3339)})

	forgotten := o.marks.forgottenBy(name)
	for namespace := range stores {
		if !forgotten[namespace] {
			return
		}
	}

	_, err := PatchPersonFinalizers(ctx, o.client, name, person.Metadata.ResourceVersion,
		person.Metadata.without(progressFinalizer))
	if errors.Is(err, ErrConflict) {
		// A write between the list and this patch is another pass's,
		// and the next pass releases again.
		return
	}
	// An object that is already gone is the state this release was for.
	if err != nil && !errors.Is(err, ErrNotFound) {
		fmt.Fprintf(os.Stderr, "releasing person %s: %v\n", name, err)
		return
	}
	o.clearTopic(forget)
	for _, namespace := range slices.Sorted(maps.Keys(forgotten)) {
		o.clearTopic(personForgottenTopic(o.topicBase, name, namespace))
	}
	o.marks.dropForgotten(name)
}

// personNamed is the Person the cluster holds under one name, or
// nothing. A name that names nobody is dropped by every caller, because
// a reference to an object that does not exist would have the garbage
// collector delete what carries it.
func personNamed(people []Person, name string) *Person {
	for index := range people {
		if people[index].Metadata.Name == name {
			return &people[index]
		}
	}
	return nil
}

// personOwner is one Person as an owner reference. There is no
// controller flag: a Play or a Watch has several owners and none of
// them manages it, and the garbage collector deletes a dependent only
// when every owner is gone.
func personOwner(person *Person) OwnerReference {
	return OwnerReference{
		APIVersion: personAPIVersion,
		Kind:       personKind,
		Name:       person.Metadata.Name,
		UID:        person.Metadata.UID,
	}
}
