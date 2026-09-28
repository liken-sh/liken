package main

// marksline.go is the set of providers the marks container asks, and what
// one file's ask learns from them: the spans, the providers that answered,
// and whether every provider the Library names answered. An answer from
// every provider is a whole attempt. An ask where one provider failed, or
// was not asked because its allowance for the day is spent, is partial, and
// the file comes back on the error window so that provider is asked again.

import (
	"context"
	"net/http"
)

// The answerers the marks container asks, in the order the Library's sources
// name their blocks, and the blocks whose allowance ran out in this run.
type markLine struct {
	answerers []markAnswerer
	spent     map[string]bool
}

// The answerer of each block the marks fact can ask. TheIntroDB takes the
// token where one reached the container, and IntroDB takes none.
var markAnswerers = map[string]func(base, token string, record *tallies) markAnswerer{
	providerBlockTheIntroDB: func(base, token string, record *tallies) markAnswerer {
		client := newTheIntroDBClient(base, token)
		client.recordTo(record)
		return newTheIntroDBMarkAnswerer(client)
	},
	providerBlockIntroDB: func(base, _ string, record *tallies) markAnswerer {
		client := newIntroDBClient(base)
		client.recordTo(record)
		return newIntroDBMarkAnswerer(client)
	},
}

// The line, in the order the Library's own spec.sources names the blocks.
func newMarkLine(blocks []string, value func(string) string, record *tallies) *markLine {
	return &markLine{answerers: recordingAnswerers(blocks, value, record, markAnswerers)}
}

// What one file's ask learned. entries are every span, in the line's order.
// held names the blocks that answered with spans, which the attempt records.
// answered names every block that answered, with spans or with none, and
// those are the blocks whose spans the ledger replaces. failure is the first
// error a block answered. complete says every block of the line answered.
type markAnswer struct {
	entries  []markEntry
	held     []string
	answered []string
	failure  error
	complete bool
}

// One file's ask: every answerer, because the marks of a file are the union
// of what the providers hold. A provider that is down does not discard the
// answers of the other blocks.
//
// A provider that answers 429 after its cooldowns has spent its allowance for
// the day. The line asks it nothing more in this run, so the run does not
// spend three requests a file on an answer that cannot change until the
// allowance resets, and every later ask of the run is partial.
func (l *markLine) ask(ctx context.Context, file markFile) markAnswer {
	answer := markAnswer{complete: true}
	for _, one := range l.answerers {
		block := one.providerBlock()
		if l.spent[block] {
			answer.complete = false
			continue
		}
		held, err := one.marks(ctx, file)
		if err != nil {
			answer.complete = false
			if answer.failure == nil {
				answer.failure = err
			}
			if answeredWith(err, http.StatusTooManyRequests) {
				if l.spent == nil {
					l.spent = map[string]bool{}
				}
				l.spent[block] = true
			}
			continue
		}
		answer.answered = append(answer.answered, block)
		if len(held) > 0 {
			answer.entries = append(answer.entries, held...)
			answer.held = append(answer.held, block)
		}
	}
	return answer
}

// Whether every provider of the line has spent its allowance, which ends the
// run: an ask now would answer nothing.
func (l *markLine) exhausted() bool {
	return len(l.answerers) > 0 && len(l.spent) >= len(l.answerers)
}

// The result an answer records. An ask no block answered is an error. An
// ask some block did not answer is partial whatever the others held, so the
// file takes the error window. A whole ask is found or nothing.
func (a markAnswer) result() string {
	switch {
	case len(a.answered) == 0:
		return attemptError
	case !a.complete:
		return attemptPartial
	case len(a.entries) > 0:
		return attemptFound
	}
	return attemptNothing
}
