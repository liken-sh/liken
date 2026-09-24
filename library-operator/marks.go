package main

// marks.go is the vocabulary of the marks fact: where a video file's intro,
// recap, credits, and preview are, as community databases record them. The
// fact stores every candidate span a provider answers, exactly as it answers
// it, and chooses none. The player in media-operator reads every candidate and
// decides which span to act on, so one rule in one place interprets them.

import "context"

// The kinds of span the fact records. A provider's own word for a kind maps
// onto one of these, so a reader sees one word per kind whatever the source.
// A post-credits span is a scene after the credits, which IntroDB records and
// TheIntroDB does not.
const (
	markKindIntro       = "intro"
	markKindRecap       = "recap"
	markKindCredits     = "credits"
	markKindPreview     = "preview"
	markKindPostCredits = "post-credits"
)

// One candidate span one provider answered for one file, as the ledger
// records it. Path is the file's entry path, the key the probe fact uses.
// Start and End are milliseconds from the start of the file. A provider that
// answers null for the start means the start of the file, and null for the
// end means the end of the file, so an absent value is not zero and the
// fields are pointers.
type markEntry struct {
	Path   string `yaml:"path"`
	Kind   string `yaml:"kind"`
	Start  *int64 `yaml:"start,omitempty"`
	End    *int64 `yaml:"end,omitempty"`
	Source string `yaml:"source"`
}

// One row of the marks table: one span of one file. Ordinal is the span's
// place among the file's spans in ledger order, which is the key beside the
// path, because two candidates of one kind from one source are two rows.
type markRow struct {
	Library string
	Path    string
	Ordinal int
	Kind    string
	Start   *int64
	End     *int64
	Source  string
}

// The file one marks ask names: the work it holds, by the ids the catalog's
// aliases carry, the aired numbers of an episode, and the file's length. A
// provider keys on the ids, and TheIntroDB reads the length to choose the
// release version whose spans fit the file.
type markFile struct {
	movie bool
	// How many episodes the file holds. A file of two episodes is asked about
	// neither.
	episodes int
	ids      providerIDs
	season   int
	episode  int
	duration int64 // milliseconds
}

// One provider block, asked for the spans of one file. A provider that holds
// none answers an empty list and no error. Every answerer is asked, because
// this fact takes the union of what the providers hold.
type markAnswerer interface {
	providerBlock() string
	marks(ctx context.Context, file markFile) ([]markEntry, error)
}

// A span as a provider answers it, before the fact names its kind and its
// source. Both providers state milliseconds, and both use null for an
// open end.
type markSpan struct {
	Start *int64 `json:"start_ms"`
	End   *int64 `json:"end_ms"`
}

// The entry one span becomes: the kind, the two ends as received, and the
// provider that answered. The fact fills the path, because the path is the
// ledger's key and not the provider's.
func (s markSpan) entry(kind, source string) markEntry {
	return markEntry{Kind: kind, Start: s.Start, End: s.End, Source: source}
}
