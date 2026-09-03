package main

// likenledger.go is the .liken/ directory beside a title: what each fact
// writes there, and how an entry is keyed. One file per writer is what lets
// several containers write beside one title on a network mount with no locks.
// No two of them ever open the same file for write.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The directory beside a title that holds liken's own files. It is a dot
// name, so the walk and every ecosystem player skip it.
const likenDirectory = ".liken"

// The entry path that names the folder's own title, as against a file under a
// season folder.
const likenSelfPath = "."

// The ids are a map of provider to id and never one column, because a title
// carries ids under several schemes and a later fact adds more.
type providerIDs map[string]string

// A numeric id is written as a number, which is the shape the plan's example
// and every provider's own documentation use.
func (p providerIDs) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	for _, provider := range sortedKeys(p) {
		value := &yaml.Node{Kind: yaml.ScalarNode, Value: p[provider]}
		if allDigits(p[provider]) {
			value.Tag = "!!int"
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: provider}, value)
	}
	return node, nil
}

// A number and a string both read back as one id, so a person who quotes an
// id by hand is read the same as the writer.
func (p *providerIDs) UnmarshalYAML(node *yaml.Node) error {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	ids := providerIDs{}
	for provider, value := range raw {
		ids[provider] = fmt.Sprint(value)
	}
	*p = ids
	return nil
}

func allDigits(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func sortedKeys(ids providerIDs) []string {
	keys := make([]string, 0, len(ids))
	for key := range ids {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// One .liken/<fact>.yaml file: the ledger the identity fact keeps, and
// the attempts every fact appends to.
type likenLedger struct {
	Items    []likenItem    `yaml:"items,omitempty"`
	Attempts []likenAttempt `yaml:"attempts,omitempty"`
}

// One item's answer: an id with the reason it was written, or the candidates
// that wait for a person.
type likenItem struct {
	Path       string           `yaml:"path"`
	ID         providerIDs      `yaml:"id,omitempty"`
	Reason     string           `yaml:"reason,omitempty"`
	Written    time.Time        `yaml:"written,omitempty"`
	Candidates []likenCandidate `yaml:"candidates,omitempty"`
}

// One candidate and its receipt, which says what matched and what did not, so
// a person chooses without repeating the search.
type likenCandidate struct {
	ID      providerIDs       `yaml:"id"`
	Title   string            `yaml:"title"`
	Year    int               `yaml:"year,omitempty"`
	Receipt map[string]string `yaml:"receipt,omitempty"`
}

// One attempt: which item, when, and how it ended.
type likenAttempt struct {
	Path   string    `yaml:"path"`
	At     time.Time `yaml:"at"`
	Result string    `yaml:"result"`
}

// A fact's file is named for the fact itself, so the one-file-per-
// writer rule is the file name.
func likenLedgerName(fact string) string {
	return fact + ".yaml"
}

// A folder with no .liken directory reads as an empty ledger and not as an
// error, because the first write to a folder starts from nothing.
func readLikenLedger(folder, fact string) (likenLedger, error) {
	data, err := os.ReadFile(filepath.Join(folder, likenDirectory, likenLedgerName(fact)))
	if errors.Is(err, fs.ErrNotExist) {
		return likenLedger{}, nil
	}
	if err != nil {
		return likenLedger{}, err
	}
	var ledger likenLedger
	if err := yaml.Unmarshal(data, &ledger); err != nil {
		return likenLedger{}, fmt.Errorf("reading %s: %w", likenLedgerName(fact), err)
	}
	return ledger, nil
}

// The whole file is read, changed, and written again through the write door.
// That is safe because one writer owns one file, and the write is a temporary
// and a rename, so a reader never sees half of it.
func (w *volumeWriter) updateLikenLedger(folder, fact string, change func(*likenLedger)) error {
	ledger, err := readLikenLedger(folder, fact)
	if err != nil {
		return err
	}
	change(&ledger)
	data, err := yaml.Marshal(ledger)
	if err != nil {
		return err
	}
	return w.writeInto(filepath.Join(folder, likenDirectory), likenLedgerName(fact), data)
}

// One path holds one attempt, the latest, so a file grows with the titles
// under a folder and never with the runs over them.
func (l *likenLedger) noteAttempt(attempt likenAttempt) {
	for at, held := range l.Attempts {
		if held.Path == attempt.Path {
			l.Attempts[at] = attempt
			return
		}
	}
	l.Attempts = append(l.Attempts, attempt)
}

// One path holds one answer. A later answer replaces the one before it,
// because the ledger says what the fact last found and not how it got
// there.
func (l *likenLedger) noteItem(item likenItem) {
	for at, held := range l.Items {
		if held.Path == item.Path {
			l.Items[at] = item
			return
		}
	}
	l.Items = append(l.Items, item)
}
