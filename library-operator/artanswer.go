package main

// The seam between one art fact and the providers that can answer it. The art
// group follows one rule: art is a single value, so the first block in the
// Library's sources that answers with an image wins, and no merge follows,
// where the nfo facts of a set take a union.

import (
	"context"
)

// One image a provider offers for one art fact: the address the fetch reads,
// the language of the text in the image, and the count of votes behind it.
// The votes are TMDb's score or Fanart.tv's likes, so they order one
// provider's list and are never read across two providers.
type artCandidate struct {
	URL      string
	Language string
	Votes    float64
}

// One provider block, asked for one gap and one art fact. It answers the
// images it holds, and a provider that holds none for that title answers no
// image and no error. The download is the provider's own, so a file takes the
// same retry rule the provider's calls take.
type artAnswerer interface {
	providerBlock() string
	serves(fact string) bool
	candidates(ctx context.Context, fact string, gap artGap, title titleRef) ([]artCandidate, error)
	fetchFile(ctx context.Context, address string) ([]byte, error)
}

// The answerers the art container can ask, in order.
type artLine struct {
	answerers []artAnswerer
}

// The line is built in the order LIBRARY_SOURCES names the blocks, which is
// the Library's own spec.sources order, and the rule for who answers reads
// that order. A block this image has no answerer for yet, and a block whose
// key did not reach the container, are both skipped with no error.
func newArtLine(blocks []string, value func(string) string) *artLine {
	line := &artLine{}
	for _, block := range blocks {
		token := value(providerTokenVariable(block))
		switch {
		case block == providerBlockTMDb && token != "":
			line.answerers = append(line.answerers, newTMDbArtAnswerer(newTMDbClient(tmdbAPIBase, token)))
		case block == providerBlockFanart && token != "":
			line.answerers = append(line.answerers, fanartArtAnswerer{client: newFanartClient(fanartAPIBase, token)})
		case block == providerBlockTVmaze:
			line.answerers = append(line.answerers, newTVmazeArtAnswerer(newTVmazeClient(tvmazeAPIBase)))
		}
	}
	return line
}

// A fact with no answerer left has nothing to ask, so the titles that remain
// keep their gaps for the next run.
func (l *artLine) live(fact string) bool {
	for _, one := range l.answerers {
		if one.serves(fact) {
			return true
		}
	}
	return false
}

// One gap's ask: every answerer that serves the fact, in order, until one of
// them holds an image, and that answerer is the one the download and the
// ledger name. An error does not end the ask, because a provider that is down
// leaves the blocks behind it their answer; the error is reported only where
// no block answered at all.
func (l *artLine) ask(ctx context.Context, fact string, gap artGap,
	title titleRef) (artAnswerer, []artCandidate, error) {
	var failure error
	for _, one := range l.answerers {
		if !one.serves(fact) {
			continue
		}
		candidates, err := one.candidates(ctx, fact, gap, title)
		if err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		if len(candidates) > 0 {
			return one, candidates, nil
		}
	}
	return nil, nil, failure
}

// The choice: the highest-voted image in the library's own language, then the
// highest-voted image with no language, then the highest-voted image of any
// language. Art with the title's own text is what a person reads on the
// screen, and art with no text reads in every language.
func chooseArt(candidates []artCandidate, language string) (artCandidate, bool) {
	for _, want := range []string{language, ""} {
		if candidate, held := bestArt(candidates, func(candidate artCandidate) bool {
			return candidate.Language == want
		}); held {
			return candidate, true
		}
	}
	return bestArt(candidates, func(artCandidate) bool { return true })
}

// The highest-voted image the test admits. The first of two equal votes wins,
// which keeps the provider's own order.
func bestArt(candidates []artCandidate, admits func(artCandidate) bool) (artCandidate, bool) {
	best := artCandidate{}
	held := false
	for _, candidate := range candidates {
		if candidate.URL == "" || !admits(candidate) {
			continue
		}
		if !held || candidate.Votes > best.Votes {
			best, held = candidate, true
		}
	}
	return best, held
}
