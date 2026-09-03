package main

// The nfo container's run of one fact. One title's work, in order: read the
// sidecar, compare the fact's element group with the hash the ledger holds,
// ask the providers, write the group, and record the answer and the attempt.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// One fact's run, bound to its name, so every nfo fact runs the same loop
// over its own gap.
func nfoFactRun(fact string) factRun {
	return func(ctx context.Context, e *enricher) error { return e.nfoFact(ctx, fact) }
}

// The answerers this container can ask, in order, and the blocks that have
// spent their day. A spent block is spent for every fact the container has
// left to run.
type answerLine struct {
	answerers []answerer
	spent     map[string]bool
}

// The line is built in the order LIBRARY_SOURCES names the blocks, which is
// the Library's own spec.sources order, and the two rules for who answers
// read that order. A block this image has no answerer for yet, and a block
// whose key did not reach the container, are both skipped with no error.
// TVmaze joins the line with no key, because it takes no account.
func newAnswerLine(blocks []string, value func(string) string) *answerLine {
	line := &answerLine{spent: map[string]bool{}}
	for _, block := range blocks {
		token := value(providerTokenVariable(block))
		switch {
		case block == providerBlockTMDb && token != "":
			line.answerers = append(line.answerers,
				tmdbAnswerer{client: newTMDbClient(tmdbAPIBase, token)})
		case block == providerBlockOMDb && token != "":
			line.answerers = append(line.answerers,
				newOMDbAnswerer(newOMDbClient(omdbAPIBase, token)))
		case block == providerBlockTVmaze:
			line.answerers = append(line.answerers,
				newTVmazeAnswerer(newTVmazeClient(tvmazeAPIBase)))
		}
	}
	return line
}

// A fact with no answerer left has nothing to ask, so the titles that remain
// keep their gaps for the next run.
func (l *answerLine) live(fact string) bool {
	for _, one := range l.answerers {
		if !l.spent[one.providerBlock()] && one.serves(fact) {
			return true
		}
	}
	return false
}

// One title's ask: every live answerer that serves the fact, in order. A
// provider that states its day is spent leaves the line, and the ask says so.
func (l *answerLine) ask(ctx context.Context, fact string, title titleRef) ([]providerAnswer, bool, error) {
	var answers []providerAnswer
	spentNow := false
	for _, one := range l.answerers {
		block := one.providerBlock()
		if l.spent[block] || !one.serves(fact) {
			continue
		}
		answer, held, err := one.answer(ctx, fact, title)
		if errors.Is(err, errDailyLimit) {
			l.spent[block], spentNow = true, true
			continue
		}
		if err != nil {
			return answers, spentNow, err
		}
		if held {
			answers = append(answers, providerAnswer{block: block, answer: answer})
		}
	}
	return answers, spentNow, nil
}

// The line is built once for the container, so a provider that spends its day
// in the first fact is not asked again in the next one. A container with no
// answerer at all is a manifest to repair, because the operator creates it
// only where a source serves one of its facts.
func (e *enricher) nfoFact(ctx context.Context, fact string) error {
	if e.providers == nil {
		e.providers = newAnswerLine(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv)
	}
	if len(e.providers.answerers) == 0 {
		return fmt.Errorf("no provider key reached this container, and the %s fact cannot ask without one", fact)
	}
	return e.nfoGap(ctx, fact, e.providers)
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run carries
// on.
func (e *enricher) nfoGap(ctx context.Context, fact string, line *answerLine) error {
	ids, err := e.gaps(ctx, fact, time.Now().UTC())
	if err != nil {
		return err
	}
	wrote, fights, left := 0, 0, 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, held, err := e.catalog.identityItem(ctx, e.library, id)
		if err != nil {
			return err
		}
		if !held || !e.inScope(item.path) {
			continue
		}
		if !line.live(fact) {
			left++
			continue
		}
		switch e.fillNFOFact(ctx, fact, line, item) {
		case attemptFight:
			fights++
		case attemptFound:
			wrote++
		case "":
			left++
		}
	}
	e.logf("wrote the %s of %d of the %d titles that lacked it, with %d held by another writer",
		fact, wrote, len(ids), fights)
	if left > 0 {
		e.logf("left the %s of %d titles for the next run, because every provider has spent its day", fact, left)
	}
	return nil
}

// One title's fill, in order: the sidecar is read, the fight check runs, the
// providers are asked, the group is written, and the answer is recorded. A
// group another writer changed stops this title and nothing else.
func (e *enricher) fillNFOFact(ctx context.Context, fact string, line *answerLine, item identityItem) string {
	folder := filepath.Join(e.root, item.path)
	sidecar, rootElement := identitySidecar(e.kind, folder)
	document, err := os.ReadFile(sidecar)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.logf("could not read the sidecar of %s: %v", item.path, err)
		e.recordNFO(folder, fact, nil, attemptError, nil)
		return attemptError
	}
	if !hasRootElement(document) {
		document = minimalNFO(rootElement, item.title)
	}

	group := nfoGroup(fact)
	if fought, err := e.groupHeldByAnother(folder, fact, group, document); err != nil {
		e.logf("could not read the %s of %s: %v", fact, item.path, err)
		e.recordNFO(folder, fact, nil, attemptError, nil)
		return attemptError
	} else if fought {
		e.logf("another writer holds the %s of %s, so this run left it", fact, item.path)
		e.recordNFO(folder, fact, nil, attemptFight, nil)
		return attemptFight
	}

	answers, spent, err := line.ask(ctx, fact, titleRef{kind: e.kind, ids: sidecarIDs(document)})
	if err != nil {
		e.logf("could not ask for the %s of %s: %v", fact, item.path, err)
		e.recordNFO(folder, fact, nil, attemptError, nil)
		return attemptError
	}
	if len(answers) == 0 {
		if spent {
			return ""
		}
		e.logf("no provider holds the %s of %s", fact, item.path)
		e.recordNFO(folder, fact, nil, attemptNothing, nil)
		return attemptNothing
	}

	merged, names := mergeAnswers(fact, answers)
	if fact == factCredits {
		merged.Cast = unionCast(sidecarCast(document), merged.Cast)
		directors, writers := sidecarCrew(document)
		merged.Directors, _ = unionPeople(directors, merged.Directors)
		merged.Writers, _ = unionPeople(writers, merged.Writers)
	}
	if !answersFact(fact, merged) {
		e.recordNFO(folder, fact, nil, attemptNothing, nil)
		return attemptNothing
	}
	return e.writeNFOFact(folder, sidecar, fact, item, group, document, merged, names)
}

// The write is the group edit and the ledger entry together. The hash the
// ledger keeps is read back off the document the edit left, so the next run
// compares like with like.
func (e *enricher) writeNFOFact(folder, sidecar, fact string, item identityItem, group elementGroup,
	document []byte, merged factAnswer, names providerNames) string {
	edited := document
	if groupNeedsWrite(fact, document, merged) {
		written, err := editElementGroup(document, group, nfoElements(fact, merged))
		if err != nil {
			e.logf("could not write the %s of %s: %v", fact, item.path, err)
			e.recordNFO(folder, fact, nil, attemptError, names)
			return attemptError
		}
		if err := e.writer.write(sidecar, written); err != nil {
			e.logf("could not write the %s of %s: %v", fact, item.path, err)
			e.recordNFO(folder, fact, nil, attemptError, names)
			return attemptError
		}
		edited = written
	}
	hash, err := groupHash(edited, group)
	if err != nil {
		e.logf("could not read back the %s of %s: %v", fact, item.path, err)
		e.recordNFO(folder, fact, nil, attemptError, names)
		return attemptError
	}
	// The credits fact writes credits.yaml and the people it names after the
	// actor elements, so a person the store has no entry for gains one on the
	// same run the .nfo names them.
	if fact == factCredits {
		e.writeCredits(folder, merged)
	}
	e.logf("wrote the %s of %s from %s", fact, item.path, strings.Join(names, ", "))
	e.recordNFO(folder, fact, &likenItem{
		Path: likenSelfPath, Provider: names, Wrote: hash, Written: time.Now().UTC(),
	}, attemptFound, names)
	return attemptFound
}

// Which facts write their group on every answer and which compare first. The
// credits fact leaves the actor, director, and writer elements where the
// merged people are what the sidecar holds, because the union added nothing a
// player reads, and credits.yaml and the .contributors/ entries are written
// either way.
func groupNeedsWrite(fact string, document []byte, merged factAnswer) bool {
	if fact != factCredits {
		return true
	}
	directors, writers := sidecarCrew(document)
	return !sameCast(sidecarCast(document), merged.Cast) ||
		!samePeople(directors, merged.Directors) ||
		!samePeople(writers, merged.Writers)
}

// The fight check compares the group on disk with the hash the ledger holds.
// A fact with no entry in its ledger has written nothing yet, so whatever the
// sidecar holds is another writer's, and this fact takes the group over.
func (e *enricher) groupHeldByAnother(folder, fact string, group elementGroup, document []byte) (bool, error) {
	ledger, err := readLikenLedger(folder, fact)
	if err != nil {
		return false, err
	}
	held, wrote := ledger.itemAt(likenSelfPath)
	if !wrote || held.Wrote == "" {
		return false, nil
	}
	hash, err := groupHash(document, group)
	if err != nil {
		return false, err
	}
	return hash != held.Wrote, nil
}

// The item entry and the attempt are one write of one file, as the identity
// fact writes them, so a reader never sees an answer without its attempt.
func (e *enricher) recordNFO(folder, fact string, entry *likenItem, result string, names providerNames) {
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		if entry != nil {
			ledger.noteItem(*entry)
		}
		ledger.noteAttempt(likenAttempt{
			Path: likenSelfPath, At: time.Now().UTC(), Result: result, Provider: names,
		})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", fact, folder, err)
	}
}

// The actors the sidecar holds, in its own order, which is the billing order
// the union starts from. A document this reader cannot parse holds no cast,
// and the fill has already recorded that as an error.
func sidecarCast(document []byte) []creditedActor {
	var read struct {
		Actors []nfoActor `xml:"actor"`
	}
	if err := lenientXML(document).Decode(&read); err != nil {
		return nil
	}
	var cast []creditedActor
	for _, actor := range read.Actors {
		name := strings.TrimSpace(actor.Name)
		if name == "" {
			continue
		}
		cast = append(cast, creditedActor{
			Name:  name,
			Role:  strings.TrimSpace(actor.Role),
			Thumb: strings.TrimSpace(actor.Thumb),
			Order: len(cast),
		})
	}
	return cast
}

// The crew the sidecar holds, in its own order, which is where the union
// starts. Kodi writes a writer into the credits element and Jellyfin into the
// writer element, so the two read as one list of writers, the way the scanner
// reads them.
func sidecarCrew(document []byte) (directors, writers []creditedPerson) {
	var read struct {
		Directors []string `xml:"director"`
		Writers   []string `xml:"writer"`
		Credits   []string `xml:"credits"`
	}
	if err := lenientXML(document).Decode(&read); err != nil {
		return nil, nil
	}
	return namedPeople(trimAll(read.Directors)), namedPeople(mergeDedup(read.Writers, read.Credits))
}

// The people one list of crew elements names. No element of the .nfo carries
// an id, so these people carry none.
func namedPeople(names []string) []creditedPerson {
	var people []creditedPerson
	for _, name := range names {
		people = append(people, creditedPerson{Name: name})
	}
	return people
}

// The ids a fact asks with come off the sidecar itself, which is where the
// identity fact wrote every one of them.
func sidecarIDs(document []byte) providerIDs {
	var read struct {
		UniqueIDs []nfoUniqueID `xml:"uniqueid"`
		IMDBID    string        `xml:"imdbid"`
		TMDBID    string        `xml:"tmdbid"`
		TVDBID    string        `xml:"tvdbid"`
		ID        string        `xml:"id"`
	}
	if err := lenientXML(document).Decode(&read); err != nil {
		return providerIDs{}
	}
	return providerIDs(collectProviders(read.UniqueIDs, read.IMDBID, read.TMDBID, read.TVDBID, read.ID))
}
