package main

// marksgap.go is the marks fact's gap: the files it asks about, and how long
// an attempt holds a file out of the gap. The community adds marks for a new
// episode or film in the days after it comes out, and the first answers are
// few and rough, so a new work is asked again sooner than an old one. The
// window follows the release date the catalog holds for the work in the
// file, and an error holds for its own one day whatever the work's age.

import (
	"strconv"
	"time"
)

// The ages that set a marks window, in days since the release, and the
// windows they set. A work in its first week is asked again after a day,
// because its marks arrive daily then. A work up to ninety days old is asked
// again after a week, because its marks still arrive as people watch it. An
// older work, or one with no release date, takes the thirty days every fact
// takes.
const (
	marksNewDays      = 7
	marksRecentDays   = 90
	marksNewWindow    = 24 * time.Hour
	marksRecentWindow = 7 * 24 * time.Hour
)

// The marks gap's two parameters beyond the ones every gap binds: the cutoff
// of the new window as ?6 and of the recent window as ?7, in Unix seconds.
func marksGapParams(now time.Time) []any {
	return []any{now.Add(-marksNewWindow).Unix(), now.Add(-marksRecentWindow).Unix()}
}

// The release of the work each file holds, keyed on the file's path, which is
// the item a marks attempt names. A file that holds two episodes takes the
// later of their dates, because the later episode is the one still gaining
// marks. A file whose works carry no date has no row.
func marksReleaseDates() string {
	return `SELECT links.library, links.path AS item, MAX(works.released) AS released ` +
		`FROM file_items AS links JOIN (` +
		`SELECT library, id, released FROM movies WHERE library = ?1 ` +
		`UNION ALL SELECT library, id, released FROM episodes WHERE library = ?1) AS works ` +
		`ON works.library = links.library AND works.id = links.item ` +
		`WHERE links.library = ?1 AND works.released != '' ` +
		`GROUP BY links.library, links.path`
}

// The marks gap reads no table of its own. Every present main video of an
// identified movie or of an episode of an identified series, with a length
// the probe measured, is asked again once its last attempt has passed that
// attempt's window, because the databases gain spans over time. The length
// is required, because TheIntroDB chooses the release version by it.
func marksGapQuery() string {
	return `SELECT path FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND role = '` + fileRolePrimary + `' AND duration_ms > 0 ` +
		`AND path IN (SELECT path FROM file_items WHERE library = ?1 AND item IN (` +
		`SELECT id FROM movies WHERE library = ?1 AND id NOT LIKE '` + scopeMovie + `:path:%' ` +
		`UNION ALL SELECT e.id FROM episodes AS e JOIN series AS s ` +
		`ON s.library = e.library AND s.id = e.series ` +
		`WHERE e.library = ?1 AND s.id NOT LIKE '` + scopeSeries + `:path:%')) ` +
		`AND (` + marksAttemptClause() + beforeReleaseClause(factMarks, "path") + `)`
}

// The attempt window of the marks fact: attemptClause's rule, with the dated
// window chosen by the release. Both sides of each date comparison are text
// that sorts as a date, as in beforeReleaseClause, and a file with no release
// row reads as neither new nor recent. The day counts are this file's
// constants and never input.
func marksAttemptClause() string {
	newSince := `date(?5, '-` + strconv.Itoa(marksNewDays) + ` days')`
	recentSince := `date(?5, '-` + strconv.Itoa(marksRecentDays) + ` days')`
	return `path NOT IN (SELECT a.item FROM attempts AS a ` +
		`LEFT JOIN (` + marksReleaseDates() + `) AS r ON r.library = a.library AND r.item = a.item ` +
		`WHERE a.library = ?1 AND a.` + attemptFactColumn + ` = '` + factMarks + `' AND a.at >= ?4 ` +
		`AND ((a.result = '` + attemptError + `' AND a.at >= ?3) ` +
		`OR (a.result != '` + attemptError + `' AND a.at >= CASE ` +
		`WHEN r.released >= ` + newSince + ` THEN ?6 ` +
		`WHEN r.released >= ` + recentSince + ` THEN ?7 ` +
		`ELSE ?2 END)))`
}
