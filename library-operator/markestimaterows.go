package main

// markestimaterows.go is the close container's step that keeps the mark
// estimates in the catalog. Nothing on the volume holds an estimate: like a
// set, it is derived from rows the catalog already holds, so a rebuilt
// catalog derives the same rows again at the close of its next Job.
//
// Every input changes inside a library Job: the probe phase writes a file's
// length, its chapters, and the dialogue of a subtitle file, the marks phase
// writes the providers' marks, and a walk adds and removes files. The close
// container runs after every phase of every library Job, so the estimates
// change in the Job that changed their inputs, and no timer reads them
// again. The step reads the whole library, because an episode's estimate
// can come from its siblings in other folders, and it writes only the rows
// that changed.

import (
	"context"
	"slices"
	"strings"
)

// The variable that names the household's subtitle languages, most
// preferred first, which choose the subtitle file the estimate reads.
const librarySubtitleLanguagesVariable = "LIBRARY_SUBTITLE_LANGUAGES"

// deriveMarkEstimates writes the estimate rows of one library from the
// catalog's files, marks, and subtitle files, and deletes the estimate rows
// whose inputs are gone.
func deriveMarkEstimates(ctx context.Context, catalog *Catalog, library string, languages []string) error {
	files, err := catalog.estimateFiles(ctx, library)
	if err != nil {
		return err
	}
	provided, held, err := catalog.estimateMarks(ctx, library)
	if err != nil {
		return err
	}
	dialogues, err := catalog.estimateDialogues(ctx, library, languages)
	if err != nil {
		return err
	}
	for at := range files {
		files[at].marks = provided[files[at].path]
		files[at].dialogue = dialogues[files[at].path]
	}
	var changed []markRow
	for _, row := range markEstimates(files) {
		key := markKey{Path: row.Path, Ordinal: row.Ordinal}
		row.Library = library
		if before, standing := held[key]; !standing || !sameEstimate(before, row) {
			changed = append(changed, row)
		}
		delete(held, key)
	}
	gone := make([]markKey, 0, len(held))
	for key := range held {
		gone = append(gone, key)
	}
	slices.SortFunc(gone, func(a, b markKey) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		return a.Ordinal - b.Ordinal
	})
	if len(changed) > 0 {
		if _, err := catalog.UpsertMarks(ctx, nil, changed); err != nil {
			return err
		}
	}
	if len(gone) > 0 {
		if _, err := catalog.DeleteMarks(ctx, library, gone); err != nil {
			return err
		}
	}
	return nil
}

func sameEstimate(a, b markRow) bool {
	return a.Kind == b.Kind && a.Source == b.Source && sameEdge(a.Start, b.Start) && sameEdge(a.End, b.End)
}

func sameEdge(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// The primary video of every movie and episode of the library that has a
// length, with the chapters the probe found and the series and season of an
// episode.
func (c *Catalog) estimateFiles(ctx context.Context, library string) ([]estimateFile, error) {
	var files []estimateFile
	err := c.stream(ctx, `SELECT files.path, files.duration_ms, files.credits_chapter_ms, `+
		`files.intro_chapter_start_ms, files.intro_chapter_end_ms, `+
		`IFNULL(MIN(episodes.series), ''), IFNULL(MIN(episodes.season), 0) FROM files `+
		`JOIN file_items ON file_items.library = files.library AND file_items.path = files.path `+
		`LEFT JOIN movies ON movies.library = file_items.library AND movies.id = file_items.item `+
		`LEFT JOIN episodes ON episodes.library = file_items.library AND episodes.id = file_items.item `+
		`WHERE files.library = ? AND files.type = '`+fileTypeVideo+`' AND files.role = '`+fileRolePrimary+`' `+
		`AND files.duration_ms > 0 AND (movies.id IS NOT NULL OR episodes.id IS NOT NULL) `+
		`GROUP BY files.path ORDER BY files.path`,
		[]any{library}, func(cells []any) error {
			if len(cells) < 7 {
				return nil
			}
			path, _ := cells[0].(string)
			series, _ := cells[5].(string)
			files = append(files, estimateFile{
				path: path, duration: cellNumber(cells[1]), chapter: cellNumber(cells[2]),
				intro:  timeSpan{Start: cellNumber(cells[3]), End: cellNumber(cells[4])},
				series: series, season: int(cellNumber(cells[6])),
			})
			return nil
		})
	return files, err
}

// The providers' credits, preview, and intro candidates of every file, and
// the estimate rows the table holds now, by their key.
func (c *Catalog) estimateMarks(ctx context.Context, library string) (map[string][]markRow, map[markKey]markRow, error) {
	provided := map[string][]markRow{}
	held := map[markKey]markRow{}
	err := c.stream(ctx, `SELECT path, ordinal, kind, start_ms, end_ms, source FROM marks `+
		`WHERE library = ? AND (ordinal < 0 OR kind IN ('`+markKindCredits+`', '`+markKindPreview+`', '`+markKindIntro+`')) `+
		`ORDER BY path, ordinal`,
		[]any{library}, func(cells []any) error {
			if len(cells) < 6 {
				return nil
			}
			path, _ := cells[0].(string)
			kind, _ := cells[2].(string)
			source, _ := cells[5].(string)
			row := markRow{Library: library, Path: path, Ordinal: int(cellNumber(cells[1])), Kind: kind,
				Start: cellMillis(cells[3]), End: cellMillis(cells[4]), Source: source}
			if row.Ordinal < 0 {
				held[markKey{Path: path, Ordinal: row.Ordinal}] = row
				return nil
			}
			provided[path] = append(provided[path], row)
			return nil
		})
	return provided, held, err
}

// A column that holds milliseconds or SQL NULL, as an edge of a mark.
func cellMillis(cell any) *int64 {
	number, held := cell.(float64)
	if !held {
		return nil
	}
	value := int64(number)
	return &value
}

// One subtitle file of a video, with what chooses it.
type subtitleChoice struct {
	path     string
	language string
	role     string
	dialogue string
}

// The dialogue of the one subtitle file each video's estimate reads. A
// subtitle file belongs to the video that shares its title. The household's
// first language that a file holds wins, then any language, and within a
// language the full track wins over a forced or a hearing-impaired one,
// whose cues leave out lines or add sounds. The least path breaks a tie, so
// two derivations choose the same file.
func (c *Catalog) estimateDialogues(ctx context.Context, library string, languages []string) (map[string]*subtitleDialogue, error) {
	choices := map[string][]subtitleChoice{}
	err := c.stream(ctx, `SELECT DISTINCT video.path, subtitle.path, subtitle.language, subtitle.role, subtitle.dialogue `+
		`FROM files AS subtitle `+
		`JOIN file_items AS spoken ON spoken.library = subtitle.library AND spoken.path = subtitle.path `+
		`JOIN file_items AS shown ON shown.library = spoken.library AND shown.item = spoken.item `+
		`JOIN files AS video ON video.library = shown.library AND video.path = shown.path `+
		`AND video.type = '`+fileTypeVideo+`' AND video.role = '`+fileRolePrimary+`' `+
		`WHERE subtitle.library = ? AND subtitle.type = '`+fileTypeSubtitle+`' AND subtitle.dialogue != ''`,
		[]any{library}, func(cells []any) error {
			if len(cells) < 5 {
				return nil
			}
			video, _ := cells[0].(string)
			choice := subtitleChoice{}
			choice.path, _ = cells[1].(string)
			choice.language, _ = cells[2].(string)
			choice.role, _ = cells[3].(string)
			choice.dialogue, _ = cells[4].(string)
			choices[video] = append(choices[video], choice)
			return nil
		})
	if err != nil {
		return nil, err
	}
	rank := func(choice subtitleChoice) int {
		if at := slices.Index(languages, choice.language); at >= 0 {
			return at
		}
		return len(languages)
	}
	dialogues := map[string]*subtitleDialogue{}
	for video, held := range choices {
		slices.SortFunc(held, func(a, b subtitleChoice) int {
			if rank(a) != rank(b) {
				return rank(a) - rank(b)
			}
			if (a.role == fileRoleFull) != (b.role == fileRoleFull) {
				if a.role == fileRoleFull {
					return -1
				}
				return 1
			}
			return strings.Compare(a.path, b.path)
		})
		dialogues[video] = dialogueColumn(held[0].dialogue)
	}
	return dialogues, nil
}
