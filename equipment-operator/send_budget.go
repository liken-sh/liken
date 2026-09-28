package main

// The send budget bounds how often the operator sends one declared
// field. The operator sends a field on each pass while the receiver
// reports another value, which drives back a change made at the
// receiver. A receiver can also take a command and keep reporting the
// old value, for example a setting its own menu locks. Without a bound
// the operator would send that command on every pass for as long as the
// spec stands. The budget allows sendLimit sends of each field at one
// declared value, then holds the field back and names it in the
// SettingsConfirmed condition.
//
// A count starts again only when its own field changes: a person
// declares another value, or the receiver reports the declared value,
// so the field is no longer pending. An edit of another field does not
// start it again. metadata.generation would, and every session flag
// and label edit would then buy a stuck field sendLimit more sends.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
)

// sendLimit is how many times the operator sends one declared field at
// one declared value.
const sendLimit = 3

// sendBudget counts the sends of each declared field, keyed by the
// field's path in the spec, and records which fields each family held
// back on its last pass.
type sendBudget struct {
	mutex sync.Mutex
	sends map[string]sendCount
	held  map[string][]string
}

// sendCount is the sends of one field at one declared value, which is
// held as JSON.
type sendCount struct {
	value string
	count int
}

func newSendBudget() *sendBudget {
	return &sendBudget{sends: map[string]sendCount{}, held: map[string][]string{}}
}

// spend answers pending without the fields that have had sendLimit
// sends at their declared value, and counts one send for each field it
// keeps. family is the path of the block in the spec, such as
// spec.denon.settings or spec.zones.zone2. A field of the family that
// is not pending loses its count. The fields it holds back replace the
// family's earlier record, so a field the receiver now reports at the
// declared value is no longer named.
func spend[T any](b *sendBudget, family string, pending T) T {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	raw, _ := json.Marshal(pending)
	var tree any
	_ = json.Unmarshal(raw, &tree)
	counted := map[string]bool{}
	var held []string
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
			held = append(held, path)
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
	b.held[family] = held
	var kept T
	pruned, _ := json.Marshal(prune(tree))
	_ = json.Unmarshal(pruned, &kept)
	return kept
}

// unconfirmed answers every field the families held back on their last
// pass, sorted, and nil when there is none.
func (b *sendBudget) unconfirmed() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	var all []string
	for _, held := range b.held {
		all = append(all, held...)
	}
	sort.Strings(all)
	return slices.Clip(all)
}

// leaves answers the path to every value in a decoded JSON tree that is
// not an object.
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

// leafValue answers the value at one path of a decoded JSON tree.
func leafValue(tree any, path []string) any {
	for _, key := range path {
		tree = tree.(map[string]any)[key]
	}
	return tree
}

// removeLeaf deletes the value at one path from a decoded JSON tree.
func removeLeaf(tree any, path []string) {
	for _, key := range path[:len(path)-1] {
		tree = tree.(map[string]any)[key]
	}
	delete(tree.(map[string]any), path[len(path)-1])
}
