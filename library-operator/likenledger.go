package main

// PROSE: this file is the .liken/ directory: what each concern writes beside a
// title, why one file per writer, and how an entry is keyed.

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

// PROSE: the directory beside a title that holds liken's own files, a dot name
// so the walk and every ecosystem player skip it.
const likenDirectory = ".liken"

// PROSE: the entry path that names the folder's own title, as against a file
// under a season folder.
const likenSelfPath = "."

// PROSE: says why the ids are a map of provider to id and never one column.
type providerIDs map[string]string

// PROSE: says why a numeric id is written as a number, which is the shape the
// plan's example and every provider's own documentation use.
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

// PROSE: says why a number and a string both read back as one id.
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

// PROSE: one .liken/<concern>.yaml file: the ledger the identity concern
// keeps, and the attempts every concern appends to.
type likenLedger struct {
	Items    []likenItem    `yaml:"items,omitempty"`
	Attempts []likenAttempt `yaml:"attempts,omitempty"`
}

// PROSE: one item's answer, which is either an id with the reason it was
// written or the candidates that wait for a person.
type likenItem struct {
	Path       string           `yaml:"path"`
	ID         providerIDs      `yaml:"id,omitempty"`
	Reason     string           `yaml:"reason,omitempty"`
	Written    time.Time        `yaml:"written,omitempty"`
	Candidates []likenCandidate `yaml:"candidates,omitempty"`
}

// PROSE: one candidate and its receipt, which says what matched and what did
// not, so a person chooses without repeating the search.
type likenCandidate struct {
	ID      providerIDs       `yaml:"id"`
	Title   string            `yaml:"title"`
	Year    int               `yaml:"year,omitempty"`
	Receipt map[string]string `yaml:"receipt,omitempty"`
}

// PROSE: one attempt: which item, when, and how it ended.
type likenAttempt struct {
	Path   string    `yaml:"path"`
	At     time.Time `yaml:"at"`
	Result string    `yaml:"result"`
}

// PROSE: says why a concern's file is named for the concern itself.
func likenLedgerName(concern string) string {
	return concern + ".yaml"
}

// PROSE: says why a folder with no .liken directory reads as an empty ledger
// rather than an error.
func readLikenLedger(folder, concern string) (likenLedger, error) {
	data, err := os.ReadFile(filepath.Join(folder, likenDirectory, likenLedgerName(concern)))
	if errors.Is(err, fs.ErrNotExist) {
		return likenLedger{}, nil
	}
	if err != nil {
		return likenLedger{}, err
	}
	var ledger likenLedger
	if err := yaml.Unmarshal(data, &ledger); err != nil {
		return likenLedger{}, fmt.Errorf("reading %s: %w", likenLedgerName(concern), err)
	}
	return ledger, nil
}

// PROSE: says why the whole file is read, changed, and written again, and why
// that is safe when one writer owns one file.
func (w *volumeWriter) updateLikenLedger(folder, concern string, change func(*likenLedger)) error {
	ledger, err := readLikenLedger(folder, concern)
	if err != nil {
		return err
	}
	change(&ledger)
	data, err := yaml.Marshal(ledger)
	if err != nil {
		return err
	}
	return w.writeInto(filepath.Join(folder, likenDirectory), likenLedgerName(concern), data)
}

// PROSE: says why one path holds one attempt, so a file grows with the titles
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

// PROSE: says why one path holds one answer, and why a later answer replaces
// the one before it.
func (l *likenLedger) noteItem(item likenItem) {
	for at, held := range l.Items {
		if held.Path == item.Path {
			l.Items[at] = item
			return
		}
	}
	l.Items = append(l.Items, item)
}
