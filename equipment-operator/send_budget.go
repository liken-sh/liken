package main

// The send budget bounds how often the operator sends one declared
// field. The operator sends a field on each pass while the receiver
// reports another value, which drives back a change made at the
// receiver. A receiver can also take a command and keep reporting the
// old value, for example a setting its own menu locks. Without a bound
// the operator would send that command on every pass for as long as the
// spec stands. The budget allows sendLimit sends of each field in one
// spec generation, then holds the field back and names it in the
// SettingsConfirmed condition. A spec change starts every count again.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
)

// sendLimit is how many times the operator sends one declared field in
// one spec generation.
const sendLimit = 3

// sendBudget counts the sends of each declared field, keyed by the
// field's path in the spec, and records which fields each family held
// back on its last pass.
type sendBudget struct {
	mutex      sync.Mutex
	generation int64
	sends      map[string]int
	held       map[string][]string
}

func newSendBudget() *sendBudget {
	return &sendBudget{sends: map[string]int{}, held: map[string][]string{}}
}

// spend answers pending without the fields that have had sendLimit
// sends in this generation, and counts one send for each field it
// keeps. family is the path of the block in the spec, such as
// spec.denon.settings or spec.zones.zone2. The fields it holds back
// replace the family's earlier record, so a field the receiver now
// reports at the declared value, which is no longer pending, is no
// longer named.
func spend[T any](b *sendBudget, generation int64, family string, pending T) T {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if generation != b.generation {
		b.generation = generation
		b.sends = map[string]int{}
		b.held = map[string][]string{}
	}
	raw, _ := json.Marshal(pending)
	var tree any
	_ = json.Unmarshal(raw, &tree)
	var held []string
	for _, leaf := range leaves(tree, nil) {
		path := family + "." + strings.Join(leaf, ".")
		if b.sends[path] >= sendLimit {
			held = append(held, path)
			removeLeaf(tree, leaf)
			continue
		}
		b.sends[path]++
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

// removeLeaf deletes the value at one path from a decoded JSON tree.
func removeLeaf(tree any, path []string) {
	for _, key := range path[:len(path)-1] {
		tree = tree.(map[string]any)[key]
	}
	delete(tree.(map[string]any), path[len(path)-1])
}
