package main

// imdbakas.go is the alternate-titles fact: the other names of a movie or a
// series, which the media browser's search matches, so a person who types
// the title a film carried in another country, or under its original
// language, finds it. IMDb's title.akas holds every title IMDb knows for each
// title id, tens of millions of rows. The fact reads the file once for its
// whole gap, as the rating and the credits read theirs, and keeps the rows of
// the gap's ids alone.
//
// The fact keeps the titles a person who reads English is likely to type:
// the original title, the titles IMDb marks as English, and the US and UK
// release titles that IMDb marks with no other language. It leaves the
// rest, such as a film's Spanish title for the US market. Search matches
// every alternate a title holds, so the cap keeps a title with many release
// names from crowding the hits of a short query.

import (
	"context"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// The dataset file the fact reads.
const datasetTitleAkas = "title.akas"

// The most alternate titles one title keeps.
const alternateTitlesCap = 5

// The regions whose titles the fact keeps where IMDb names no language: a
// title IMDb lists for the US or the UK is the name a film was released
// under there.
var alternateRegions = map[string]bool{"US": true, "GB": true}

// One alternate title as the fact's ledger records it: the title, the region
// and the language IMDb gives for it, and whether it is the original title.
type alternateTitle struct {
	Title    string `yaml:"title"`
	Region   string `yaml:"region,omitempty"`
	Language string `yaml:"language,omitempty"`
	Original bool   `yaml:"original,omitempty"`
}

// One row of title.akas the read keeps, in the file's order.
type akaRow struct {
	title    string
	region   string
	language string
	original bool
}

// Whether a row is one of the titles the fact keeps.
func (r akaRow) wanted() bool {
	return r.original || r.language == "en" || (r.language == "" && alternateRegions[r.region])
}

// The columns of title.akas: titleId, ordering, title, region, language,
// types, attributes, isOriginalTitle.
func akaOf(cells [][]byte) akaRow {
	return akaRow{
		title:    strings.TrimSpace(datasetCell(cells, 2)),
		region:   datasetCell(cells, 3),
		language: datasetCell(cells, 4),
		original: datasetCell(cells, 7) == "1",
	}
}

// The alternates of one title: the original title first, then the other
// rows in IMDb's order, less any that says what the catalog's title or an
// earlier alternate already says, up to the cap.
func chooseAlternates(rows []akaRow, title string) []alternateTitle {
	said := map[string]bool{titleKey(title): true}
	var chosen []alternateTitle
	keep := func(row akaRow) {
		key := titleKey(row.title)
		if !row.wanted() || key == "" || said[key] || len(chosen) == alternateTitlesCap {
			return
		}
		said[key] = true
		chosen = append(chosen, alternateTitle{Title: row.title, Region: row.region,
			Language: row.language, Original: row.original})
	}
	for _, row := range rows {
		if row.original {
			keep(row)
		}
	}
	for _, row := range rows {
		if !row.original {
			keep(row)
		}
	}
	return chosen
}

// The letters and digits of a title in lower case. Search folds a query the
// same way, so two titles with one key match the same queries, and the
// second one adds nothing.
func titleKey(title string) string {
	var key strings.Builder
	for _, letter := range title {
		if unicode.IsLetter(letter) || unicode.IsDigit(letter) {
			key.WriteRune(unicode.ToLower(letter))
		}
	}
	return key.String()
}

// The fact's run. A read that fails ends the fact for the rest of the
// container, as a failed rating read does, and the titles keep their gap
// for the next run with no attempt.
func (e *enricher) alternateTitlesFact(ctx context.Context) error {
	if e.akasUnread {
		return nil
	}
	ids, err := e.gaps(ctx, factAlternateTitles, time.Now().UTC())
	if err != nil || len(ids) == 0 {
		return err
	}
	aliases, err := e.imdbAliases(ctx)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if imdb := aliases[id]; imdb != "" {
			wanted[imdb] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	started := time.Now()
	held := map[string]bool{}
	rows := map[string][]akaRow{}
	modified, err := e.datasetFetcher().read(ctx, datasetTitleAkas, func(cells [][]byte) {
		id := datasetCell(cells, 0)
		if !wanted[id] {
			return
		}
		held[id] = true
		if row := akaOf(cells); row.wanted() {
			rows[id] = append(rows[id], row)
		}
	})
	if err != nil {
		e.akasUnread = true
		e.logf("could not read the IMDb datasets, so this run leaves the %s gap: reading %s: %v",
			factAlternateTitles, datasetTitleAkas, err)
		return nil
	}
	e.logf("read %s in %s and found %d of the %d titles the gap asks for", datasetTitleAkas,
		time.Since(started).Round(time.Millisecond), len(held), len(wanted))
	written := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		imdb := aliases[id]
		if imdb == "" {
			continue
		}
		item, present, err := e.catalog.identityItem(ctx, e.library, id)
		if err != nil {
			return err
		}
		if !present || !e.inScope(item.path) {
			continue
		}
		alternates := chooseAlternates(rows[imdb], item.title)
		e.recordAlternateTitles(item, held[imdb], alternates, modified)
		if len(alternates) > 0 {
			written++
		}
	}
	e.logf("wrote the %s of %d of the %d titles that lacked them", factAlternateTitles, written, len(ids))
	return nil
}

// The list and the attempt, in one write of the fact's ledger. A title
// title.akas holds is found, even where every row it holds was left out,
// because the next read of the file says the same. A title the file does
// not hold is nothing, and the attempt window asks again later.
func (e *enricher) recordAlternateTitles(item identityItem, held bool, alternates []alternateTitle,
	modified time.Time) {
	result := attemptNothing
	var names providerNames
	if held {
		result, names = attemptFound, providerNames{providerBlockIMDb}
	}
	e.tallies.add(tallyAttempts, 1, "fact", factAlternateTitles, "result", result)
	folder := filepath.Join(e.root, item.path)
	err := e.writer.updateLikenLedger(folder, factAlternateTitles, func(ledger *likenLedger) {
		ledger.AlternateTitles = alternates
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: time.Now().UTC(), Result: result,
			Provider: names, DatasetModified: modified})
	})
	if err != nil {
		e.logf("could not record the %s of %s: %v", factAlternateTitles, opaqueID(item.id), err)
		return
	}
	e.writeRows(factAlternateTitles, folder, held)
}

// The gap: every movie and series with an IMDb id and no found attempt. A
// title with no IMDb id is out of the gap, because only the imdb block
// serves the fact. A found title is not asked again on a timer, because a
// film's titles rarely change, and a refresh asks again.
func alternateTitlesGapQuery() string {
	imdb := `id IN (SELECT item FROM aliases WHERE aliases.library = ?1 ` +
		`AND (alias GLOB '` + scopeMovie + `:imdb:*' OR alias GLOB '` + scopeSeries + `:imdb:*'))`
	missing := `id NOT IN (SELECT item FROM attempts WHERE attempts.library = ?1 ` +
		`AND ` + attemptFactColumn + ` = '` + factAlternateTitles + `' AND result = '` + attemptFound + `')`
	return `SELECT id FROM (` +
		`SELECT library, id FROM movies WHERE id NOT LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id FROM series WHERE id NOT LIKE 'series:path:%') AS items ` +
		`WHERE library = ?1 AND ` + imdb + ` AND ` + gapClause(factAlternateTitles, "id", missing)
}

// The alternate titles one folder's ledger holds for its own title, less any
// that the title the .nfo file names already says. The title can change after
// the fact ran, so the walk checks again.
func alternatesOf(entries []alternateTitle, title string) []string {
	said := map[string]bool{titleKey(title): true}
	var titles []string
	for _, entry := range entries {
		key := titleKey(entry.Title)
		if key == "" || said[key] {
			continue
		}
		said[key] = true
		titles = append(titles, entry.Title)
	}
	return titles
}
