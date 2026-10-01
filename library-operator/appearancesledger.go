package main

// appearancesledger.go is the appearances fact's ledger, .liken/appearances.yaml
// beside the video files it describes. The appearances tool writes the
// detections record and prints the match document. The worker reads that
// document and writes this file, so the ledger, its answer, and its attempts
// are one file with one writer, as every other .liken ledger is.
//
// Each entry holds everything its answer depends on: the size of the file the
// faces came from, the embedder, the threshold and the margin, and each
// person of the gallery with the hash of their headshot. A reader compares
// those with the volume to tell that an answer is stale, and applies a higher
// threshold or margin to the similarities without a new match.

import "gopkg.in/yaml.v3"

// One video file's answer: the faces the match named in it, at keyframe
// times, and the inputs the names came from.
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
	Unmatched    []appearancesPerson     `yaml:"unmatched,omitempty"`
	Observations []appearanceObservation `yaml:"observations"`
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

// One face the match named: the keyframe's time in seconds, the face's place
// in that keyframe's line of the detections record, the person, and the
// similarity. The next closest person's similarity is absent in a gallery of
// one, which is not the same as a similarity of zero.
type appearanceObservation struct {
	Time        float64  `json:"time" yaml:"time"`
	Face        int      `json:"face" yaml:"face"`
	Contributor string   `json:"contributor" yaml:"contributor"`
	Similarity  float32  `json:"similarity" yaml:"similarity"`
	RunnerUp    *float32 `json:"runner_up,omitempty" yaml:"runnerUp,omitempty"`
}

// A film holds hundreds of observations, so each one, each person, and each
// model is one line in flow style. A person reads one face per line, and grep
// finds every face of one person.
func (o appearanceObservation) MarshalYAML() (any, error) {
	type plain appearanceObservation
	return flowNode(plain(o))
}

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
