package main

// scanwrite.go writes a walk's rows to the catalog and removes what a title or
// a file left behind. The scanner is the one writer of its agent's catalog, so
// the set of ids and paths the last walk wrote is the whole record of what the
// volume held. A full walk compares that record to the new one and deletes the
// difference. A single-path rescan upserts and merges, and leaves the removals
// to the next full walk, because it read one path and cannot tell what left the
// rest of the volume.

import "context"

// catalogState is the record of what the last walk wrote: the item ids per
// kind, the file rows by path, and the alias keys. A full walk reads it to find
// what left the volume, and replaces it.
type catalogState struct {
	movies   map[string]bool
	series   map[string]bool
	episodes map[string]bool
	files    map[string]fileRow
	aliases  map[string]bool
}

// newCatalogState builds an empty record, the state a scanner holds before its
// first walk.
func newCatalogState() catalogState {
	return catalogState{
		movies:   map[string]bool{},
		series:   map[string]bool{},
		episodes: map[string]bool{},
		files:    map[string]fileRow{},
		aliases:  map[string]bool{},
	}
}

// stateOf reads the ids, paths, and aliases a walk produced into a record.
func stateOf(result *walkResult) catalogState {
	state := newCatalogState()
	for _, row := range result.movies {
		state.movies[row.Id] = true
	}
	for _, row := range result.series {
		state.series[row.Id] = true
	}
	for _, row := range result.episodes {
		state.episodes[row.Id] = true
	}
	for _, row := range result.files {
		state.files[row.Path] = row
	}
	for _, row := range result.aliases {
		state.aliases[row.Alias] = true
	}
	return state
}

// upsertWalk writes every row a walk produced: the items, the files and their
// item links, and the aliases. It reports whether it wrote anything.
func upsertWalk(ctx context.Context, catalog *Catalog, result *walkResult) (bool, error) {
	wrote := false
	steps := []func() (int, error){
		func() (int, error) { return catalog.UpsertMovies(ctx, result.movies) },
		func() (int, error) { return catalog.UpsertSeries(ctx, result.series) },
		func() (int, error) { return catalog.UpsertEpisodes(ctx, result.episodes) },
		func() (int, error) { return catalog.UpsertFiles(ctx, result.files) },
		func() (int, error) { return catalog.UpsertFileItems(ctx, result.files) },
		func() (int, error) { return catalog.UpsertAliases(ctx, result.aliases) },
	}
	for _, step := range steps {
		applied, err := step()
		if err != nil {
			return wrote, err
		}
		if applied > 0 {
			wrote = true
		}
	}
	return wrote, nil
}

// applyFull writes a whole-root walk and reconciles the catalog to it. It
// upserts every row, then deletes the ids, paths, and aliases the previous walk
// held and this one does not. It returns the new record and whether the
// volume's structure changed, which is what moves the report's last-change
// time. A write that fails leaves the record as it was, so the next walk
// retries the removals.
func applyFull(ctx context.Context, catalog *Catalog, previous catalogState, result *walkResult) (catalogState, bool, error) {
	if _, err := upsertWalk(ctx, catalog, result); err != nil {
		return previous, false, err
	}
	current := stateOf(result)
	changed := false

	if removed := removedKeys(previous.movies, current.movies); len(removed) > 0 {
		if _, err := catalog.DeleteMovies(ctx, removed); err != nil {
			return previous, false, err
		}
		changed = true
	}
	if removed := removedKeys(previous.series, current.series); len(removed) > 0 {
		if _, err := catalog.DeleteSeries(ctx, removed); err != nil {
			return previous, false, err
		}
		changed = true
	}
	if removed := removedKeys(previous.episodes, current.episodes); len(removed) > 0 {
		if _, err := catalog.DeleteEpisodes(ctx, removed); err != nil {
			return previous, false, err
		}
		changed = true
	}
	if removed := removedFiles(previous.files, current.files); len(removed) > 0 {
		if _, err := catalog.DeleteFiles(ctx, filePaths(removed)); err != nil {
			return previous, false, err
		}
		if _, err := catalog.DeleteFileItems(ctx, removed); err != nil {
			return previous, false, err
		}
		changed = true
	}
	if removed := removedKeys(previous.aliases, current.aliases); len(removed) > 0 {
		if _, err := catalog.DeleteAliases(ctx, removed); err != nil {
			return previous, false, err
		}
		changed = true
	}

	if additions(previous, current) {
		changed = true
	}
	return current, changed, nil
}

// applyPartial writes a single-path rescan and merges it into the record. It
// never deletes, because a rescan reads one path and cannot tell what left the
// rest of the volume. It marks a change when it wrote anything, because a
// webhook is an import worth reporting at once.
func applyPartial(ctx context.Context, catalog *Catalog, previous catalogState, result *walkResult) (catalogState, bool, error) {
	wrote, err := upsertWalk(ctx, catalog, result)
	if err != nil {
		return previous, false, err
	}
	merged := mergeState(previous, stateOf(result))
	return merged, wrote, nil
}

// removedKeys returns the keys the previous record held and the current one
// does not.
func removedKeys(previous, current map[string]bool) []string {
	var removed []string
	for key := range previous {
		if !current[key] {
			removed = append(removed, key)
		}
	}
	return removed
}

// removedFiles returns the file rows a previous walk held and this one does
// not, so the delete carries the item links each departed file held.
func removedFiles(previous, current map[string]fileRow) []fileRow {
	var removed []fileRow
	for path, row := range previous {
		if _, held := current[path]; !held {
			removed = append(removed, row)
		}
	}
	return removed
}

// filePaths reads the paths off a set of file rows.
func filePaths(rows []fileRow) []string {
	paths := make([]string, len(rows))
	for i, row := range rows {
		paths[i] = row.Path
	}
	return paths
}

// additions reports whether the current record holds an id, path, or alias the
// previous one did not, the other half of a structural change.
func additions(previous, current catalogState) bool {
	return len(removedKeys(current.movies, previous.movies)) > 0 ||
		len(removedKeys(current.series, previous.series)) > 0 ||
		len(removedKeys(current.episodes, previous.episodes)) > 0 ||
		len(removedFiles(current.files, previous.files)) > 0 ||
		len(removedKeys(current.aliases, previous.aliases)) > 0
}

// mergeState folds a rescan's record into the standing one, so a later full
// walk still knows the rows a rescan wrote.
func mergeState(base, added catalogState) catalogState {
	for key := range added.movies {
		base.movies[key] = true
	}
	for key := range added.series {
		base.series[key] = true
	}
	for key := range added.episodes {
		base.episodes[key] = true
	}
	for path, row := range added.files {
		base.files[path] = row
	}
	for key := range added.aliases {
		base.aliases[key] = true
	}
	return base
}
