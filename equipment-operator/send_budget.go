package main

// On each pass, the operator sends every declared field whose value
// differs from what the receiver reports, which undoes a change made at
// the receiver. A receiver can accept a command and keep reporting the
// old value, for example when its own menu locks the setting, and
// without a limit the operator would send that command on every pass.
// So the send budget allows sendLimit sends of each field at one
// declared value. After that the operator stops sending the field and
// lists it in the SettingsConfirmed condition.
//
// A field's count restarts only when that field changes: a person
// declares another value, or the receiver reports the declared value.
// Restarting every count when metadata.generation changes would give a
// stuck field sendLimit more sends on each edit of another field, such
// as a session flag.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
)

// sendLimit is the number of times the operator sends one field at one
// declared value.
const sendLimit = 3

// sendBudget counts the sends of each declared field, keyed by the
// field's path in the spec. unsent records, for each family, the fields
// that the last pass did not send because they reached sendLimit.
type sendBudget struct {
	mutex  sync.Mutex
	sends  map[string]sendCount
	unsent map[string][]string
}

// sendCount is the number of sends of one field at one declared value.
// The value is stored as JSON.
type sendCount struct {
	value string
	count int
}

func newSendBudget() *sendBudget {
	return &sendBudget{sends: map[string]sendCount{}, unsent: map[string][]string{}}
}

// spend returns pending without the fields that already had sendLimit
// sends at their declared value, and counts one send for each field it
// returns. family is the path of the block in the spec, such as
// spec.denon.settings or spec.zones.zone2. A field of the family that
// is no longer pending loses its count, and the family's list of unsent
// fields is replaced, so a field the receiver now reports at the
// declared value leaves the SettingsConfirmed condition.
func spend[T any](b *sendBudget, family string, pending T) T {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	raw, _ := json.Marshal(pending)
	var tree any
	_ = json.Unmarshal(raw, &tree)
	counted := map[string]bool{}
	var unsent []string
	for _, leaf := range leaves(tree, nil) {
		path := family + "." + strings.Join(leaf, ".")
		value, _ := json.Marshal(leafValue(tree, leaf))
		sent := b.sends[path]
		if sent.value != string(value) {
			sent = sendCount{value: string(value)}
		}
		counted[path] = true
		if sent.count >= sendLimit {
			b.sends[path] = sent
			unsent = append(unsent, path)
			removeLeaf(tree, leaf)
			continue
		}
		sent.count++
		b.sends[path] = sent
	}
	for path := range b.sends {
		if strings.HasPrefix(path, family+".") && !counted[path] {
			delete(b.sends, path)
		}
	}
	b.unsent[family] = unsent
	var kept T
	pruned, _ := json.Marshal(prune(tree))
	_ = json.Unmarshal(pruned, &kept)
	return kept
}

// unconfirmed returns the fields that the last pass of each family did
// not send because they reached sendLimit, sorted. It returns nil when
// there are none.
func (b *sendBudget) unconfirmed() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	var all []string
	for _, fields := range b.unsent {
		all = append(all, fields...)
	}
	sort.Strings(all)
	return slices.Clip(all)
}

// leaves returns the path to every value in a decoded JSON tree that
// is not an object.
func leaves(tree any, prefix []string) [][]string {
	object, isObject := tree.(map[string]any)
	if !isObject {
		return [][]string{slices.Clone(prefix)}
	}
	var found [][]string
	for key, value := range object {
		found = append(found, leaves(value, append(prefix, key))...)
	}
	sort.Slice(found, func(i, j int) bool { return strings.Join(found[i], ".") < strings.Join(found[j], ".") })
	return found
}

func leafValue(tree any, path []string) any {
	for _, key := range path {
		tree = tree.(map[string]any)[key]
	}
	return tree
}

func removeLeaf(tree any, path []string) {
	for _, key := range path[:len(path)-1] {
		tree = tree.(map[string]any)[key]
	}
	delete(tree.(map[string]any), path[len(path)-1])
}
