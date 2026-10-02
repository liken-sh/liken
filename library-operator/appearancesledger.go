package main

// appearancesledger.go is the appearances fact's ledger, .liken/appearances.yaml
// beside the video files it describes. The appearances tool writes the
// detections record, and each video's matches file and spans file, and
// prints a summary. The worker reads that summary and writes this file, so
// the ledger, its answer, and its attempts are one file with one writer, as
// every other .liken ledger is.
//
// Each entry holds everything its answer depends on: the size of the file the
// faces came from, the embedder, the threshold and the margin, and each
// person of the gallery with the hash of their headshot. A reader compares
// those with the volume to tell that an answer is stale. The faces
// themselves are in the matches file, .liken/appearances/<file>.matches.json,
// because a film holds thousands of them and the walk parses every ledger of
// a folder on each pass. The entry holds their counts.

import "gopkg.in/yaml.v3"

// One video file's answer: how many faces the match named in it, and the
// inputs the names came from.
type appearancesEntry struct {
	Path string `yaml:"path"`
	// The size in bytes of the file the detections record came from, which
	// fileidentity.go compares with the probe record.
	Size      int64           `yaml:"size"`
	Embedder  detectionsModel `yaml:"embedder"`
	Threshold float32         `yaml:"threshold"`
	Margin    float32         `yaml:"margin"`
	// Every credited actor the match compared faces with, in billing order.
	Gallery []appearancesPerson `yaml:"gallery"`
	// The people who cannot match: an entry with no headshot, or a headshot
	// the detector found no face in. The list makes a gap in the answer
	// visible.
	Unmatched []appearancesPerson `yaml:"unmatched,omitempty"`
	Named     appearancesNamed    `yaml:"named"`
	// How many different people the faces name.
	People int `yaml:"people"`
}

// How many faces each pass of the match named. The headshot pass names a
// face that reaches the threshold and the margin against the headshots. The
// film pass names a face from the faces of the same film that the headshot
// pass named.
type appearancesNamed struct {
	Headshot int `json:"headshot" yaml:"headshot"`
	Film     int `json:"film" yaml:"film"`
}

func (n appearancesNamed) MarshalYAML() (any, error) {
	type plain appearancesNamed
	return flowNode(plain(n))
}

// A model as the tool names it: the file's name, and the SHA-256 of its
// bytes. Vectors from two embedders cannot be compared, so the hash says
// which answers can be compared with each other.
type detectionsModel struct {
	Name   string `json:"name" yaml:"name"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// One person of the gallery: their entry in .contributors/, their name, and
// whether their headshot gave a face. The hash of the headshot file is
// absent where the entry holds none, and a reader compares it with the file
// to tell that the headshot changed.
type appearancesPerson struct {
	Contributor string `json:"contributor" yaml:"contributor"`
	Name        string `json:"name" yaml:"name"`
	Headshot    string `json:"headshot" yaml:"headshot"`
	SHA256      string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
}

// A gallery holds dozens of people, so each person and each model is one
// line in flow style.
func (p appearancesPerson) MarshalYAML() (any, error) {
	type plain appearancesPerson
	return flowNode(plain(p))
}

func (m detectionsModel) MarshalYAML() (any, error) {
	type plain detectionsModel
	return flowNode(plain(m))
}

func flowNode(value any) (*yaml.Node, error) {
	node := &yaml.Node{}
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	node.Style = yaml.FlowStyle
	return node, nil
}

// One path holds one answer, the latest, as every list in a ledger does.
func (l *likenLedger) noteAppearances(entry appearancesEntry) {
	for at, held := range l.Appearances {
		if held.Path == entry.Path {
			l.Appearances[at] = entry
			return
		}
	}
	l.Appearances = append(l.Appearances, entry)
}
