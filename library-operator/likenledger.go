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
	Items []likenItem `yaml:"items,omitempty"`
	// The credits fact's own list, in the file that is its ledger: one entry per
	// credited person, with the directory in .contributors/ that holds that
	// person. Only the credits fact writes it, so the list, the answer, and the
	// attempts are one write of one file.
	Credits []creditEntry `yaml:"credits,omitempty"`
	// The arrival fact's own list, in the file that is its ledger: one entry
	// per video file with the time it arrived. Only the arrival fact writes it,
	// and the walk reads it for the added and arrived columns.
	Files    []arrivalEntry `yaml:"files,omitempty"`
	Attempts []likenAttempt `yaml:"attempts,omitempty"`
}

// The provider blocks that answered one fact: one name for a single value,
// and a list for a set the fact took the union of. A person reads either form
// back the same way.
type providerNames []string

func (p providerNames) MarshalYAML() (any, error) {
	if len(p) == 1 {
		return p[0], nil
	}
	return []string(p), nil
}

func (p *providerNames) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var name string
		if err := node.Decode(&name); err != nil {
			return err
		}
		*p = providerNames{name}
		return nil
	}
	var names []string
	if err := node.Decode(&names); err != nil {
		return err
	}
	*p = names
	return nil
}

// One item as a ledger records it. Provider is which provider answered, and
// Wrote is the hash of the element group the fact left in the .nfo. The next
// run compares the group on disk with that hash, so a group another writer
// changed is a fight and not an overwrite.
type likenItem struct {
	Path string `yaml:"path"`
	// Which provider answered for this item, so a person reads why the file
	// looks the way it does. An art fact writes existing here for a file another
	// tool had already written.
	Provider providerNames `yaml:"provider,omitempty"`
	ID       providerIDs   `yaml:"id,omitempty"`
	Reason   string        `yaml:"reason,omitempty"`
	// Source is where the bytes came from, for a fact that writes a file from
	// a link. The franchise art fetch keys on it: the same link is never read
	// again, and a changed link is.
	Source     string           `yaml:"source,omitempty"`
	Wrote      string           `yaml:"wrote,omitempty"`
	Written    time.Time        `yaml:"written,omitempty"`
	Candidates []likenCandidate `yaml:"candidates,omitempty"`
}

// One name reads back as the one provider that answered, so a fact that takes
// a single value asks whether that block wrote the item.
func (p providerNames) is(name string) bool {
	return len(p) == 1 && p[0] == name
}

// One candidate and its receipt, which says what matched and what did not, so
// a person chooses without repeating the search.
type likenCandidate struct {
	ID      providerIDs       `yaml:"id"`
	Title   string            `yaml:"title"`
	Year    int               `yaml:"year,omitempty"`
	Receipt map[string]string `yaml:"receipt,omitempty"`
}

// One attempt as a ledger records it. Provider names the provider blocks the
// attempt asked, empty for a fact that asks none, and the scanner lifts it
// into the attempts table.
type likenAttempt struct {
	Path     string        `yaml:"path"`
	At       time.Time     `yaml:"at"`
	Result   string        `yaml:"result"`
	Provider providerNames `yaml:"provider,omitempty"`
}

// A fact's file is named for the fact itself, so the one-file-per-
// writer rule is the file name.
func likenLedgerName(fact string) string {
	return fact + ".yaml"
}

// One item's entry out of a fact's ledger, or false where the ledger holds
// none for that path.
func (l *likenLedger) itemAt(path string) (likenItem, bool) {
	for _, item := range l.Items {
		if item.Path == path {
			return item, true
		}
	}
	return likenItem{}, false
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
