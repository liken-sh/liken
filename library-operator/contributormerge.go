package main

// The merge of two or more .contributors/ entries that hold one id, which the
// contributor.ids fact runs after its own gap. The ids fact is the writer of
// contributor.yaml after the credits fact creates it, and its fight check
// compares the file with the hash in its ledger, so the merge is the ids
// fact's work and its ledger records the hash of every file the merge writes.
// The credits fact moves the credits that name a removed entry, in
// creditsmove.go, because it is the one writer of credits.yaml.

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The merge gap: every entry that holds an id another entry of the library
// holds, and every entry a merge removed that no credit names. An entry the
// merge left, because a person edited it or because it holds another id in
// one scheme, is in the gap again only after the window of its merge attempt.
// Every such entry is a gap whatever its own columns say, so the missing
// condition is always true.
func contributorMergeGapSQL() string {
	return `SELECT path FROM (` +
		`SELECT a.path AS path FROM contributor_ids AS a ` +
		`JOIN (SELECT scheme, id FROM contributor_ids WHERE library = ?1 ` +
		`GROUP BY scheme, id HAVING count(*) > 1) AS shared ` +
		`ON shared.scheme = a.scheme AND shared.id = a.id ` +
		`WHERE a.library = ?1 ` +
		`UNION SELECT path FROM contributor_merges WHERE library = ?1 ` +
		`AND path NOT IN (SELECT contributor FROM credits WHERE credits.library = ?1)` +
		`) AS entries WHERE ` + gapClause(factContributorMerge, "path", "1 = 1")
}

// One entry of a group, as the volume holds it: its path relative to the
// library root, its directory, and its contributor.yaml with the bytes the
// fight check hashes.
type groupEntry struct {
	path   string
	folder string
	file   contributorFile
	data   []byte
}

// The counts one pass of the merge gap logs.
type mergeCounts struct {
	merged, held, conflicts, deleted int
}

// A catalog read that fails ends the container, because the gap list is the
// work. One group that fails records an error attempt, and the run goes on.
func (e *enricher) mergeContributors(ctx context.Context) error {
	paths, err := e.catalog.queryStrings(ctx, gapQueries[factContributorMerge],
		gapParams(factContributorMerge, e.library, time.Now().UTC(), time.Time{}))
	if err != nil {
		return fmt.Errorf("reading the %s gap of %s: %w", factContributorMerge, e.library, err)
	}
	counts := mergeCounts{}
	done := map[string]bool{}
	for _, entry := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if done[entry] || !e.inScope(entry) {
			continue
		}
		group, record, err := e.contributorGroup(ctx, entry)
		if err != nil {
			return err
		}
		done[entry] = true
		if record {
			if e.deleteMergedEntry(ctx, entry) {
				counts.deleted++
			}
			continue
		}
		for _, member := range group {
			done[member.path] = true
		}
		if len(group) > 1 {
			e.mergeGroup(ctx, group, &counts)
		}
	}
	e.logf("merged %d groups of entries that hold one id, left %d entries a person edited, "+
		"found %d groups with two ids in one scheme, and deleted %d merged entries",
		counts.merged, counts.held, counts.conflicts, counts.deleted)
	return nil
}

// Every entry that shares an id with the first one, directly or through
// another entry, as the volume holds them. The catalog names the entries that
// hold each id, and the volume says which ids each of them holds now. A first
// entry that is a merge record answers true and no group.
func (e *enricher) contributorGroup(ctx context.Context, first string) ([]groupEntry, bool, error) {
	var group []groupEntry
	queue := []string{first}
	seen := map[string]bool{first: true}
	for len(queue) > 0 {
		entry := queue[0]
		queue = queue[1:]
		folder := filepath.Join(e.root, entry)
		held, data, err := readContributorFile(filepath.Join(folder, contributorFileName))
		if err != nil {
			e.logf("could not read the entry of %s: %v", entry, err)
			e.recordMerge(folder, attemptError, "")
			continue
		}
		if data == nil {
			continue
		}
		if held.MergedInto != "" {
			if entry == first {
				return nil, true, nil
			}
			continue
		}
		group = append(group, groupEntry{path: entry, folder: folder, file: held, data: data})
		for _, scheme := range sortedKeys(held.IDs) {
			others, err := e.catalog.contributorsWithID(ctx, e.library, scheme, held.IDs[scheme])
			if err != nil {
				return nil, false, fmt.Errorf("reading the entries of %s %s: %w", scheme, held.IDs[scheme], err)
			}
			for _, other := range others {
				if !seen[other] {
					seen[other] = true
					queue = append(queue, other)
				}
			}
		}
	}
	// The order of the catalog's answers is not the order of the volume, so the
	// group reads in path order, and a reason names its entries the same way on
	// every run.
	slices.SortFunc(group, func(a, b groupEntry) int { return strings.Compare(a.path, b.path) })
	return group, false, nil
}

// One group's merge, in order: the fight check, the check for two ids in one
// scheme, the choice of the entry that stays, the write of the joined ids, the
// move of the files, and the record in each removed entry.
func (e *enricher) mergeGroup(ctx context.Context, group []groupEntry, counts *mergeCounts) {
	for _, member := range group {
		fought, err := e.contributorHeldByAnother(member.folder, member.data)
		if err != nil {
			e.logf("could not read the ledger of %s: %v", member.path, err)
			e.recordGroup(group, attemptError, "")
			return
		}
		if fought {
			reason := "a person edited " + path.Join(member.path, contributorFileName)
			e.logf("left the entries %s, because %s", groupPaths(group), reason)
			e.recordGroup(group, attemptHeld, reason)
			counts.held += len(group)
			return
		}
	}
	ids, conflict := joinedIDs(group)
	if conflict != "" {
		e.logf("left the entries %s, because %s", groupPaths(group), conflict)
		e.recordGroup(group, attemptConflict, conflict)
		counts.conflicts++
		return
	}
	stays := entryToKeep(group)
	if err := e.writeMergedEntries(group, stays, ids); err != nil {
		e.logf("could not merge %s into %s: %v", groupPaths(group), stays.path, err)
		e.recordGroup(group, attemptError, "")
		return
	}
	// The files are the truth, and the next walk writes the same rows, so a
	// catalog that refuses them leaves the merge as it is.
	if err := e.writeMergeRows(ctx, group, stays); err != nil {
		e.logf("could not write the rows of the merge into %s: %v", stays.path, err)
	}
	e.logf("merged %s into %s", groupPaths(group), stays.path)
	e.recordMerge(stays.folder, attemptFound, "")
	counts.merged++
	for _, member := range group {
		if member.path != stays.path && e.deleteMergedEntry(ctx, member.path) {
			counts.deleted++
		}
	}
}

// Every id of the group, and a reason where two entries hold two different ids
// in one scheme.
func joinedIDs(group []groupEntry) (providerIDs, string) {
	ids := providerIDs{}
	holder := map[string]string{}
	for _, member := range group {
		for _, scheme := range sortedKeys(member.file.IDs) {
			id := member.file.IDs[scheme]
			if id == "" {
				continue
			}
			if held, found := ids[scheme]; found && held != id {
				return nil, fmt.Sprintf("%s holds %s %s and %s holds %s %s",
					holder[scheme], scheme, held, member.path, scheme, id)
			}
			ids[scheme], holder[scheme] = id, member.path
		}
	}
	return ids, ""
}

// The entry that stays: the entry at the plain slug of its own name, then the
// entry with the most ids, then the first path, so every run chooses the same
// entry.
func entryToKeep(group []groupEntry) groupEntry {
	stays := group[0]
	for _, member := range group[1:] {
		if keepsBefore(member, stays) {
			stays = member
		}
	}
	return stays
}

func keepsBefore(a, b groupEntry) bool {
	if plainA, plainB := a.atPlainSlug(), b.atPlainSlug(); plainA != plainB {
		return plainA
	}
	if len(a.file.IDs) != len(b.file.IDs) {
		return len(a.file.IDs) > len(b.file.IDs)
	}
	return a.path < b.path
}

// Whether the entry's directory is the slug of its own name with no id suffix.
func (g groupEntry) atPlainSlug() bool {
	return path.Base(g.path) == contributorSlug(g.file.Name, g.file.IDs)
}

func groupPaths(group []groupEntry) string {
	paths := make([]string, len(group))
	for i, member := range group {
		paths[i] = member.path
	}
	return strings.Join(paths, ", ")
}

// The writes of one merge. The entry that stays gets every id, and a date it
// lacks from the others. Each removed entry gives its biography and headshot
// to the entry that stays where that entry has none, and keeps a
// contributor.yaml that names the entry that stays. The ids fact's ledger of
// each entry records the hash of what the merge wrote, so the fact's next run
// does not read the merge as a hand edit.
func (e *enricher) writeMergedEntries(group []groupEntry, stays groupEntry, ids providerIDs) error {
	filled := stays.file
	filled.IDs = ids
	for _, member := range group {
		if filled.Born == "" {
			filled.Born = member.file.Born
		}
		if filled.Died == "" {
			filled.Died = member.file.Died
		}
	}
	if err := e.writeMergeFile(stays.folder, stays.data, filled); err != nil {
		return err
	}
	for _, member := range group {
		if member.path == stays.path {
			continue
		}
		for _, name := range []string{contributorBiographyName, contributorHeadshotName} {
			if _, err := e.moveHeldFile(member.folder, stays.folder, name); err != nil {
				return err
			}
		}
		if err := e.writeMergeFile(member.folder, member.data, contributorFile{MergedInto: stays.path}); err != nil {
			return err
		}
	}
	return nil
}

// One file of the merge, and its hash in the ids fact's ledger. A file the
// merge does not change is left as it is.
func (e *enricher) writeMergeFile(folder string, data []byte, file contributorFile) error {
	written := marshalContributorFile(file)
	if string(written) != string(data) {
		if err := e.writer.write(filepath.Join(folder, contributorFileName), written); err != nil {
			return err
		}
	}
	return e.writer.updateLikenLedger(folder, factContributorIDs, func(ledger *likenLedger) {
		item, _ := ledger.itemAt(likenSelfPath)
		item.Path, item.Wrote = likenSelfPath, contentHash(written)
		ledger.noteItem(item)
	})
}

// A file the removed entry does not hold is no move.
func (e *enricher) moveHeldFile(from, to, name string) (bool, error) {
	held, err := fileExists(filepath.Join(from, name))
	if err != nil || !held {
		return false, err
	}
	return e.writer.moveContributorFile(filepath.Join(from, name), filepath.Join(to, name))
}

// The rows of one merge, written at once, so the credits fact finds the
// removed entries in the same run and the lookup by id reaches the entry that
// stays. The removed entries leave the people and the ids, and each becomes a
// merge record. The ids of the removed entries leave before the entry that
// stays writes its own, so every alias of the group resolves to that entry.
func (e *enricher) writeMergeRows(ctx context.Context, group []groupEntry, stays groupEntry) error {
	var removed []string
	var records []contributorMergeRow
	for _, member := range group {
		if member.path != stays.path {
			removed = append(removed, member.path)
			records = append(records, contributorMergeRow{Library: e.library, Path: member.path, MergedInto: stays.path})
		}
	}
	result := &walkResult{}
	readContributorFolder(e.root, e.library, stays.folder, result)
	steps := []func() (int, error){
		func() (int, error) { return e.catalog.DeletePersonIDs(ctx, e.library, removed) },
		func() (int, error) { return e.catalog.DeleteContributors(ctx, e.library, removed) },
		func() (int, error) { return e.catalog.UpsertContributorMerges(ctx, records) },
		func() (int, error) { return e.catalog.UpdateContributorFacts(ctx, result.contributors) },
		func() (int, error) { return e.catalog.UpsertContributorIDs(ctx, result.contributorAliases) },
	}
	for _, step := range steps {
		if _, err := step(); err != nil {
			return err
		}
	}
	return nil
}

// The delete of one removed entry, once no credit names it. An entry a credit
// still names stays until the credits fact moves that credit. The answer says
// whether the entry left the volume.
func (e *enricher) deleteMergedEntry(ctx context.Context, entry string) bool {
	folder := filepath.Join(e.root, entry)
	named, err := e.catalog.creditsNaming(ctx, e.library, entry)
	if err != nil {
		e.logf("could not read the credits of %s: %v", entry, err)
		return false
	}
	if named > 0 {
		return false
	}
	if err := e.writer.removeMergedEntry(folder); err != nil {
		e.logf("could not delete the merged entry %s: %v", entry, err)
		e.recordMerge(folder, attemptError, "")
		return false
	}
	if _, err := e.catalog.DeleteContributorMerges(ctx, e.library, []string{entry}); err != nil {
		e.logf("could not delete the merge record of %s: %v", entry, err)
	}
	return true
}

// One attempt for every entry of a group, so the gap leaves the whole group
// for the attempt's window.
func (e *enricher) recordGroup(group []groupEntry, result, reason string) {
	for _, member := range group {
		e.recordMerge(member.folder, result, reason)
	}
}

// The merge's own ledger in one entry, with the reason a person reads where
// the merge left the entry.
func (e *enricher) recordMerge(folder, result, reason string) {
	e.tallies.add(tallyAttempts, 1, "fact", factContributorMerge, "result", result)
	err := e.writer.updateLikenLedger(folder, factContributorMerge, func(ledger *likenLedger) {
		ledger.Items = nil
		if reason != "" {
			ledger.noteItem(likenItem{Path: likenSelfPath, Reason: reason})
		}
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: time.Now().UTC(), Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", factContributorMerge, folder, err)
	}
	e.writeRows(factContributorMerge, folder, false)
}
