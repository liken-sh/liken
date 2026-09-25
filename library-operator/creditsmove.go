package main

// The move of the credits that name a .contributors/ entry a merge removed,
// which the credits fact runs after its own gap. The credits fact is the one
// writer of each title's credits.yaml, so it moves the credits, and the
// contributor.ids fact deletes the removed entry once no credit names it.

import (
	"context"
	"path/filepath"
	"slices"
	"time"
)

// The move gap: every title with a credit that names an entry a merge removed.
// The credits index on (library, contributor) reaches those titles from the
// merge records. Every such title is a gap whatever its own columns say, so
// the missing condition is always true.
func creditsMoveGapSQL() string {
	return `SELECT DISTINCT c.item FROM contributor_merges AS m ` +
		`JOIN credits AS c ON c.library = m.library AND c.contributor = m.path ` +
		`WHERE m.library = ?1 AND ` + gapClause(factCreditsMove, "c.item", "1 = 1")
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run goes on.
func (e *enricher) moveCredits(ctx context.Context) error {
	ids, err := e.gaps(ctx, factCreditsMove, time.Now().UTC())
	if err != nil {
		return err
	}
	moved := 0
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
		if e.moveTitleCredits(filepath.Join(e.root, item.path)) {
			moved++
		}
	}
	e.logf("moved the credits of %d of the %d titles that named a merged entry", moved, len(ids))
	return nil
}

// One title's move: each credit that names a removed entry names the entry
// that stays. A credit whose record names no entry on the volume keeps the path
// it has, and the attempt records that nothing moved.
func (e *enricher) moveTitleCredits(folder string) bool {
	now := time.Now().UTC()
	ledger, err := readLikenLedger(folder, factCredits)
	if err != nil {
		e.logf("could not read the credits of %s: %v", relativePath(e.root, folder), err)
		e.recordAttempt(folder, factCreditsMove, likenSelfPath, attemptError, now)
		return false
	}
	credits := slices.Clone(ledger.Credits)
	changed := false
	for at, credit := range credits {
		if credit.Contributor == "" {
			continue
		}
		if stays := e.entryThatStays(credit.Contributor); stays != "" && stays != credit.Contributor {
			credits[at].Contributor = stays
			changed = true
		}
	}
	if !changed {
		e.recordAttempt(folder, factCreditsMove, likenSelfPath, attemptNothing, now)
		return false
	}
	err = e.writer.updateLikenLedger(folder, factCredits, func(ledger *likenLedger) {
		ledger.Credits = credits
	})
	if err != nil {
		e.logf("could not write the credits of %s: %v", relativePath(e.root, folder), err)
		e.recordAttempt(folder, factCreditsMove, likenSelfPath, attemptError, now)
		return false
	}
	e.recordAttempt(folder, factCreditsMove, likenSelfPath, attemptFound, now)
	return true
}
