package main

// sets.go derives the sets item table. A set is the collection a movie
// sidecar names, a film and its sequels, and nothing on the volume holds it,
// so every set row is derived from the movies that name it. A full walk
// derives each set from all of its members as they arrive. A rescan reads
// one folder and cannot see a set's other members, so it derives the set
// again from the movie rows the catalog holds.

import (
	"context"
	"encoding/json"
	"sort"
)

// scopeSet is the word that leads every set id, beside the scopes in
// rows.go.
const scopeSet = "set"

// setBody is empty, because a set holds nothing beyond the item header. The
// empty struct marshals to the {} the body column holds by default.
type setBody struct{}

// setRow is one row of the sets item table: the item header every kind
// carries, with the empty body.
type setRow struct {
	Id       string
	Library  string
	Kind     string
	Path     string
	Title    string
	SortKey  string
	Slug     string
	Released string
	Added    int64
	Art      string
	Duration int64
	Body     setBody
}

// setID derives a set's id the way itemID derives a movie's: from the
// tmdbcolid the set element carries, or from the slug of the set's name where
// the element carries no id. A set with neither has no id, and the movie that
// names it belongs to no set.
func setID(collectionID, name string) string {
	if collectionID != "" {
		return scopeSet + ":tmdb:" + collectionID
	}
	key := slug(name, 0)
	if key == "" {
		return ""
	}
	return scopeSet + ":name:" + key
}

// setMember is the one movie a set derives its row from: the earliest
// released of its members. The movie's own id breaks a tie on the date, so
// the fold and the catalog read below pick the same member whatever order the
// walk's workers read the folders in. The added field is the earliest arrival
// among every member, which is not always the earliest-released one's.
type setMember struct {
	set      string
	movie    string
	library  string
	name     string
	released string
	art      string
	added    int64
}

// earlier reports whether this member replaces the one the fold holds.
func (m setMember) earlier(than setMember) bool {
	if m.released != than.released {
		return m.released < than.released
	}
	return m.movie < than.movie
}

// row is the set row this member derives. The set has no path, because
// nothing on the volume holds it, and its slug carries no year, because a
// set gains members over time and a year would move.
func (m setMember) row() setRow {
	return setRow{
		Id:       m.set,
		Library:  m.library,
		Kind:     libraryKindMovies,
		Title:    m.name,
		SortKey:  sortKey(m.name),
		Slug:     slug(m.name, 0),
		Released: m.released,
		Added:    m.added,
		Art:      m.art,
	}
}

// setFold holds the earliest member of every set the walk has read so far.
// It lives for the whole walk, because two members of one set can land in
// different write batches, and a set derived per batch would be overwritten
// by the batch that held only its later member. It holds one entry per set,
// never the whole library.
type setFold map[string]setMember

// add folds one folder's movie rows in.
func (f setFold) add(movies []movieRow) {
	for _, movie := range movies {
		if movie.SetID == "" {
			continue
		}
		candidate := setMember{
			set:      movie.SetID,
			movie:    movie.Id,
			library:  movie.Library,
			name:     movie.Body.Collection,
			released: movie.Released,
			art:      movie.Art,
			added:    movie.Added,
		}
		held, exists := f[movie.SetID]
		if exists {
			candidate.added = earliestArrival(held.added, candidate.added)
			if !candidate.earlier(held) {
				held.added = candidate.added
				f[movie.SetID] = held
				continue
			}
		}
		f[movie.SetID] = candidate
	}
}

// rows is the fold as set rows, in id order, so a walk writes the same rows
// in the same order every time.
func (f setFold) rows() []setRow {
	rows := make([]setRow, 0, len(f))
	for _, id := range sortedSetIDs(f) {
		rows = append(rows, f[id].row())
	}
	return rows
}

// sortedSetIDs is the fold's keys in order.
func sortedSetIDs(f setFold) []string {
	ids := make([]string, 0, len(f))
	for id := range f {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// uniqueSetIDs is the list with the empty id and every repeat removed, in
// order, so a reconciliation reads and writes each set one time.
func uniqueSetIDs(ids []string) []string {
	held := map[string]bool{}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || held[id] {
			continue
		}
		held[id] = true
		unique = append(unique, id)
	}
	sort.Strings(unique)
	return unique
}

// setIDsOf is the set ids a folder's movie rows name.
func setIDsOf(movies []movieRow) []string {
	var ids []string
	for _, movie := range movies {
		if movie.SetID != "" {
			ids = append(ids, movie.SetID)
		}
	}
	return ids
}

// reconcileSets derives each named set again from the movie rows the
// catalog holds for it, and deletes a set with no member left. A rescan
// takes this step because it reads one folder and not the set's other
// members, and because the scoped prune cannot reach a set row: a set has an
// empty path, which is outside every folder's range.
func reconcileSets(ctx context.Context, catalog *Catalog, library string, ids []string) error {
	for _, id := range uniqueSetIDs(ids) {
		member, found, err := catalog.earliestSetMember(ctx, library, id)
		if err != nil {
			return err
		}
		if !found {
			if _, err := catalog.DeleteSets(ctx, library, []string{id}); err != nil {
				return err
			}
			continue
		}
		if _, err := catalog.UpsertSets(ctx, []setRow{member.row()}); err != nil {
			return err
		}
	}
	return nil
}

// earliestSetMember reads the member a set derives its row from, in the
// order the fold uses. The set's name comes off that movie's body, where the
// sidecar's set name lands.
func (c *Catalog) earliestSetMember(ctx context.Context, library, set string) (setMember, bool, error) {
	member := setMember{set: set, library: library}
	found := false
	err := c.stream(ctx, earliestSetMemberSQL(), []any{library, set}, func(cells []any) error {
		if found || len(cells) < 4 {
			return nil
		}
		member.released, _ = cells[0].(string)
		member.art, _ = cells[1].(string)
		// The query API answers JSON, so an integer column arrives as a
		// float64.
		if added, ok := cells[2].(float64); ok {
			member.added = int64(added)
		}
		body, _ := cells[3].(string)
		var decoded movieBody
		if err := json.Unmarshal([]byte(body), &decoded); err == nil {
			member.name = decoded.Collection
		}
		found = true
		return nil
	})
	return member, found, err
}

// earliestSetMemberSQL reads one set's earliest member through the
// movies_library_set_id index, and beside it the earliest arrival over every
// member, where an arrival of zero means none is known.
func earliestSetMemberSQL() string {
	return `SELECT released, art,` +
		` (SELECT min(CASE WHEN added > 0 THEN added END) FROM movies WHERE library = ?1 AND set_id = ?2),` +
		` body FROM movies` +
		` WHERE library = ?1 AND set_id = ?2 ORDER BY released, id LIMIT 1`
}

// setIDsUnder reads the sets the movies of one title folder name, before a
// rescan of that folder writes it again. A movie the rescan moves out of its
// set, or removes, can leave that set without a member.
func (c *Catalog) setIDsUnder(ctx context.Context, library, folder string) ([]string, error) {
	params := []any{library}
	params = append(params, pathScopeParams(folder)...)
	params = append(params, pruneBatch)
	return c.queryStrings(ctx, setIDsUnderSQL(), params)
}

// setIDsUnderSQL scopes the read to one title folder, the same range over
// the path the scoped prune reads.
func setIDsUnderSQL() string {
	return `SELECT set_id FROM movies WHERE library = ? AND ` + pathScopeClause("path") +
		` AND set_id <> '' LIMIT ?`
}
