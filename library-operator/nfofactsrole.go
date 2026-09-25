package main

// The nfo container's run of one fact. One title's work, in order: read the
// .nfo file, compare the fact's element group with the hash the ledger holds,
// ask the providers, write the group, and record the answer and the attempt.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
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

// The answerer of each block the nfo facts can ask. TVmaze is built with no
// key, because it takes no account.
var nfoAnswerers = map[string]func(base, token string, record *tallies) answerer{
	providerBlockTMDb: func(base, token string, record *tallies) answerer {
		client := newTMDbClient(base, token)
		client.recordTo(record)
		return tmdbAnswerer{client: client}
	},
	providerBlockOMDb: func(base, token string, record *tallies) answerer {
		client := newOMDbClient(base, token)
		client.recordTo(record)
		return newOMDbAnswerer(client)
	},
	providerBlockTVmaze: func(base, _ string, record *tallies) answerer {
		client := newTVmazeClient(base)
		client.recordTo(record)
		return newTVmazeAnswerer(client)
	},
}

// The line the two rules for who answers read, in the order the Library's own
// spec.sources names the blocks.
func newAnswerLine(blocks []string, value func(string) string, record *tallies) *answerLine {
	return &answerLine{
		answerers: recordingAnswerers(blocks, value, record, nfoAnswerers),
		spent:     map[string]bool{},
	}
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
		if errors.Is(err, errDailyLimit) || errors.Is(err, errDatasetsUnread) {
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
		e.providers = e.nfoAnswerLine()
		e.personFinder = newPersonFinder(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv, e.tallies)
	}
	if len(e.providers.answerers) == 0 {
		return fmt.Errorf("no provider key reached this container, and the %s fact cannot ask without one", fact)
	}
	if err := e.nfoGap(ctx, fact, e.providers); err != nil {
		return err
	}
	// A merge of two .contributors/ entries leaves credits that name the entry
	// it removed. The credits fact is the one writer of credits.yaml, so it
	// moves them after its own gap.
	if fact == factCredits {
		return e.moveCredits(ctx)
	}
	return nil
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run carries
// on.
func (e *enricher) nfoGap(ctx context.Context, fact string, line *answerLine) error {
	ids, err := e.gaps(ctx, fact, time.Now().UTC())
	if err != nil {
		return err
	}
	switch fact {
	case factRatingIMDb:
		e.coverDatasetGap(ctx, ids)
	case factCredits:
		e.coverCreditGap(ctx, ids)
	}
	wrote, fights, left := 0, 0, 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		// An episode's rating is the imdb block's alone, so it takes its own path.
		if isEpisodeID(id) {
			switch e.fillEpisodeRating(ctx, id) {
			case attemptFight:
				fights++
			case attemptFound:
				wrote++
			}
			continue
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
		e.logf("left the %s of %d titles for the next run, because no provider can answer again in this run", fact, left)
	}
	return nil
}

// One title's fill, in order: the .nfo file is read, the fight check runs, the
// providers are asked, the group is written, and the answer is recorded. A
// group another writer changed stops this title and nothing else.
func (e *enricher) fillNFOFact(ctx context.Context, fact string, line *answerLine, item identityItem) string {
	folder := filepath.Join(e.root, item.path)
	nfoPath, rootElement := identityNFO(e.kind, folder)
	document, err := os.ReadFile(nfoPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.logf("could not read the .nfo file of %s: %v", item.path, err)
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

	answers, spent, err := line.ask(ctx, fact, titleRef{kind: e.kind, ids: nfoIDs(document)})
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
		merged = creditsOrNFO(merged, document)
	}
	if !answersFact(fact, merged) {
		e.recordNFO(folder, fact, nil, attemptNothing, nil)
		return attemptNothing
	}
	return e.writeNFOFact(folder, nfoPath, fact, item, group, document, merged, names)
}

// A provider that named any person is the whole cast and crew, and the
// people in the .nfo file stay only where no provider named one.
// Jellyfin writes a producer as an actor element whose role is
// "Producer" and whose type element is absent, so a union keeps the
// producer in the cast and names a person who acts and produces twice.
// A word list on the role is wrong, because a library holds a character
// named "Director".
func creditsOrNFO(merged factAnswer, document []byte) factAnswer {
	if len(merged.Cast) > 0 || len(merged.Directors) > 0 || len(merged.Writers) > 0 {
		return merged
	}
	merged.Cast = nfoCast(document)
	merged.Directors, merged.Writers = nfoCrew(document)
	return merged
}

// The write is the group edit and the ledger entry together. The hash the
// ledger keeps is read back off the document the edit left, so the next run
// compares like with like.
//
// The probe and identity containers edit the same .nfo file while this fact
// asks its providers. So the edit takes the file's lock, reads the file
// again, and changes this fact's group in what it reads, and an element
// another container wrote in the meantime stays.
func (e *enricher) writeNFOFact(folder, nfoPath, fact string, item identityItem, group elementGroup,
	document []byte, merged factAnswer, names providerNames) string {
	release, err := lockNFO(e.writer.locks, nfoPath)
	if err != nil {
		e.logf("could not write the %s of %s: %v", fact, item.path, err)
		e.recordNFO(folder, fact, nil, attemptError, names)
		return attemptError
	}
	defer release()
	if fresh, err := os.ReadFile(nfoPath); err == nil && hasRootElement(fresh) {
		document = fresh
	}
	edited := document
	if groupNeedsWrite(fact, document, merged) {
		written, err := editElementGroup(document, group, nfoElements(fact, merged))
		if err != nil {
			e.logf("could not write the %s of %s: %v", fact, item.path, err)
			e.recordNFO(folder, fact, nil, attemptError, names)
			return attemptError
		}
		if err := e.writer.write(nfoPath, written); err != nil {
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
// IMDb rating compares its value, as ratingChanged says. The
// credits fact leaves the actor, director, and writer elements where
// credits.yaml and the .contributors/ entries are written either way.
// The credits fact rewrites nothing where the people it holds are the
// people the .nfo file holds.
func groupNeedsWrite(fact string, document []byte, merged factAnswer) bool {
	if fact == factRatingIMDb {
		return ratingChanged(document, merged)
	}
	if fact != factCredits {
		return true
	}
	directors, writers := nfoCrew(document)
	return !sameCast(nfoCast(document), merged.Cast) ||
		!samePeople(directors, merged.Directors) ||
		!samePeople(writers, merged.Writers)
}

// The fight check compares the group on disk with the hash the ledger holds.
// A fact with no entry in its ledger has written nothing yet, so whatever the
// .nfo file holds is another writer's, and this fact rewrites the group.
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
	e.tallies.add(tallyAttempts, 1, "fact", fact, "result", result)
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		if entry != nil {
			ledger.noteItem(*entry)
		}
		ledger.noteAttempt(likenAttempt{
			Path: likenSelfPath, At: time.Now().UTC(), Result: result, Provider: names,
			DatasetModified: e.datasetTimeOf(fact, names),
		})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", fact, folder, err)
	}
	e.writeRows(fact, folder, result == attemptFound)
}

// The actors the .nfo file holds, in billing order. An actor element with an
// order gets that place, and one without
// follows every actor that has one, in document order. A document this reader
// cannot parse holds no cast, and the fill has already recorded that as an
// error.
// They are the cast where no provider named one.
func nfoCast(document []byte) []creditedActor {
	var read struct {
		Actors []nfoActor `xml:"actor"`
	}
	if err := lenientXML(document).Decode(&read); err != nil {
		return nil
	}
	sort.SliceStable(read.Actors, func(i, j int) bool {
		return billingOf(read.Actors[i]) < billingOf(read.Actors[j])
	})
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

// The billing an actor element states, and a place after every stated one
// for an element that states none.
func billingOf(actor nfoActor) int {
	if actor.Order == nil {
		return math.MaxInt
	}
	return *actor.Order
}

// The crew the .nfo file holds, in its own order. Kodi writes a writer into the
// credits element and Jellyfin into the
// writer element, so the two read as one list of writers, the way the scanner
// reads them.
// They are the crew where no provider named one.
func nfoCrew(document []byte) (directors, writers []creditedPerson) {
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

// The ids that a fact asks with come from the .nfo file itself, which is where
// the identity fact wrote every one of them.
func nfoIDs(document []byte) providerIDs {
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
