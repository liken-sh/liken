use super::*;

// A double S01E01-E02 of 2,640 seconds, so each part runs 1,320 seconds,
// and a triple S01E01-E03 of 3,960 seconds.
const DOUBLE: i64 = 2_640;
const TRIPLE: i64 = 3_960;

// The file of `parts` episodes S01E01 upward that a row naming `part`
// reads, with the probe's duration and no credits marks.
fn file(parts: i64, part: i64, duration: i64) -> File {
    File {
        parts,
        part,
        duration,
        episodes: (1..=parts).map(|at| (at, 1, at)).collect(),
        credits: Vec::new(),
    }
}

// One row of the store as a `Play` on a screen writes it: a position and a
// duration in the file, and the numbers of the episode it names.
fn row(play: &str, episode: i64, position: i64, duration: i64) -> Progress {
    Progress {
        play: play.into(),
        position,
        duration,
        recorded: 50,
        running: true,
        season: 1,
        episode,
        origin: Origin::Screen,
        ..Progress::default()
    }
}

// What a screen reads of one split row: the name, the episode, the
// position, the duration, and whether it finished.
type Read = (String, i64, i64, i64, bool);

fn read(rows: Vec<Progress>) -> Vec<Read> {
    rows.into_iter()
        .map(|row| {
            (
                row.play,
                row.episode,
                row.position,
                row.duration,
                row.finished,
            )
        })
        .collect()
}

fn named(play: &str, episode: i64, position: i64, duration: i64, finished: bool) -> Read {
    (play.into(), episode, position, duration, finished)
}

// Every stage of a play of the double in the plan's table, and the triple.
// The time rule finishes an episode of 1,320 seconds at 1,254 seconds.
#[test]
fn a_row_splits_into_the_parts_it_covers() {
    let cases = [
        (
            "play on E01 at 600",
            row("den", 1, 600, DOUBLE),
            file(2, 1, DOUBLE),
            vec![named("den#01", 1, 600, 1_320, false)],
        ),
        (
            "play on E01 at 1,300",
            row("den", 1, 1_300, DOUBLE),
            file(2, 1, DOUBLE),
            vec![named("den#01", 1, 1_300, 1_320, true)],
        ),
        (
            "play on E01 at 1,800",
            row("den", 1, 1_800, DOUBLE),
            file(2, 1, DOUBLE),
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 480, 1_320, false),
            ],
        ),
        (
            "play on E01 to the end",
            row("den", 1, DOUBLE, DOUBLE),
            file(2, 1, DOUBLE),
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 1_320, 1_320, true),
            ],
        ),
        (
            "play on E02 alone at 1,800",
            row("den", 2, 1_800, DOUBLE),
            file(2, 2, DOUBLE),
            vec![named("den#02", 2, 480, 1_320, false)],
        ),
        (
            "a position exactly on the boundary",
            row("den", 1, 1_320, DOUBLE),
            file(2, 1, DOUBLE),
            vec![named("den#01", 1, 1_320, 1_320, true)],
        ),
        (
            "a triple at 3,000",
            row("den", 1, 3_000, TRIPLE),
            file(3, 1, TRIPLE),
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 1_320, 1_320, true),
                named("den#03", 3, 360, 1_320, false),
            ],
        ),
        (
            "a play past the end of the file",
            row("den", 1, 2_700, DOUBLE),
            file(2, 1, DOUBLE),
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 1_320, 1_320, true),
            ],
        ),
    ];
    for (case, row, file, want) in cases {
        assert_eq!(read(split(row, &file)), want, "{case}");
    }
}

// The rows that the split leaves as they are, and the rows it answers
// nothing or 0 for.
#[test]
fn the_split_keeps_or_drops_a_row_by_the_plans_table() {
    let mark = Progress {
        origin: Origin::Mark,
        ..row("mark-den-1", 2, 1_320, 1_320)
    };
    let cases = [
        (
            "a file of one work",
            row("den", 1, 600, 1_320),
            file(1, 1, 1_320),
            vec![named("den", 1, 600, 1_320, false)],
        ),
        (
            "a link no scan has split",
            row("den", 1, 1_300, 1_320),
            file(0, 0, 0),
            vec![named("den", 1, 1_300, 1_320, true)],
        ),
        (
            "a file with no duration",
            row("den", 2, 600, 0),
            file(2, 2, 0),
            vec![named("den", 2, 600, 0, false)],
        ),
        (
            "a mark",
            mark,
            file(2, 2, DOUBLE),
            vec![named("mark-den-1", 2, 1_320, 1_320, true)],
        ),
        (
            "a clear of E02",
            row("jellyfin-user-item", 2, 0, DOUBLE),
            file(2, 2, DOUBLE),
            vec![named("jellyfin-user-item#02", 2, 0, 1_320, false)],
        ),
        (
            "a play on E02 at a second of E01",
            row("den", 2, 1_000, DOUBLE),
            file(2, 2, DOUBLE),
            vec![],
        ),
        (
            "a play on E02 at its first second",
            row("den", 2, 1_320, DOUBLE),
            file(2, 2, DOUBLE),
            vec![named("den#02", 2, 0, 1_320, false)],
        ),
    ];
    for (case, row, file, want) in cases {
        assert_eq!(read(split(row, &file)), want, "{case}");
    }
}

// A probe that found no duration leaves the row's own duration as the
// file's, so a Jellyfin row of a file the probe could not read still
// splits.
#[test]
fn the_rows_duration_stands_in_for_a_probe_with_none() {
    let rows = split(row("den", 1, 1_800, DOUBLE), &file(2, 1, 0));
    assert_eq!(
        read(rows),
        [
            named("den#01", 1, 1_320, 1_320, true),
            named("den#02", 2, 480, 1_320, false),
        ]
    );
}

// The parts of an odd duration start at the floor of their share, and the
// last part takes the second that is left.
#[test]
fn an_odd_duration_gives_the_last_part_the_remainder() {
    let rows = split(row("den", 1, 2_641, 2_641), &file(2, 1, 2_641));
    assert_eq!(
        read(rows),
        [
            named("den#01", 1, 1_320, 1_320, true),
            named("den#02", 2, 1_321, 1_321, true),
        ]
    );
}

// A part with no numbered episode still holds its seconds, and the read
// answers no row for it.
#[test]
fn a_part_with_no_episode_answers_no_row() {
    let mut triple = file(3, 1, TRIPLE);
    triple.episodes.retain(|(at, _, _)| *at != 2);
    let rows = split(row("den", 1, 3_000, TRIPLE), &triple);
    assert_eq!(
        read(rows),
        [
            named("den#01", 1, 1_320, 1_320, true),
            named("den#03", 3, 360, 1_320, false),
        ]
    );
}

// Every split row keeps the play's time, its origin, and whether it runs,
// so `Origin::of` and the thread rule read it as the play it came from.
#[test]
fn each_part_keeps_the_plays_own_facts() {
    let jellyfin = Progress {
        origin: Origin::Outside,
        running: false,
        ..row("jellyfin-user-item", 1, 1_800, DOUBLE)
    };
    let rows = split(jellyfin, &file(2, 1, DOUBLE));
    let facts: Vec<(Origin, bool, i64)> = rows
        .iter()
        .map(|row| (row.origin, row.running, row.recorded))
        .collect();
    assert_eq!(facts, [(Origin::Outside, false, 50); 2]);
}

// A credits mark near the end of the file moves only the last part's line,
// and the earlier part keeps the time rule.
#[test]
fn credits_marks_move_into_the_part_they_start_in() {
    let mut double = file(2, 1, DOUBLE);
    double.credits = vec![Credits {
        start: Some(2_600.0),
        end: None,
    }];
    let cases = [
        (1_254, vec![named("den#01", 1, 1_254, 1_320, true)]),
        (
            2_590,
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 1_270, 1_320, false),
            ],
        ),
        (
            2_600,
            vec![
                named("den#01", 1, 1_320, 1_320, true),
                named("den#02", 2, 1_280, 1_320, true),
            ],
        ),
    ];
    for (position, want) in cases {
        let rows = split(row("den", 1, position, DOUBLE), &double);
        assert_eq!(read(rows), want, "{position}");
    }
}

// A mark of E01 holds the episode's own seconds, so the credits of E02 at
// the end of the file do not keep it from reading as finished.
#[test]
fn a_mark_reads_the_credits_of_its_own_episode() {
    let mut double = file(2, 1, DOUBLE);
    double.credits = vec![Credits {
        start: Some(2_600.0),
        end: Some(2_640.0),
    }];
    let mark = Progress {
        origin: Origin::Mark,
        ..row("mark-den-1", 1, 1_320, 1_320)
    };
    assert!(split(mark, &double)[0].finished);
}

// The thread rule breaks a tie in recorded time on the name, so the later
// part must sort last.
#[test]
fn the_names_of_one_plays_parts_sort_in_part_order() {
    let rows = split(row("den", 1, 3_000, TRIPLE), &file(3, 1, TRIPLE));
    let names: Vec<&str> = rows.iter().map(|row| row.play.as_str()).collect();
    let mut sorted = names.clone();
    sorted.sort();
    assert_eq!(names, sorted);
}

// Where a play of each episode starts, and how long it runs to the end of
// its file.
#[test]
fn a_place_answers_the_start_and_the_run_of_an_episode() {
    let place = |part, parts, duration| Place {
        part,
        parts,
        duration,
        last: (1, parts),
    };
    let cases = [
        (
            "E01 of a double",
            place(1, 2, DOUBLE),
            Some(0),
            Some(DOUBLE),
        ),
        (
            "E02 of a double",
            place(2, 2, DOUBLE),
            Some(1_320),
            Some(1_320),
        ),
        (
            "E03 of a triple",
            place(3, 3, TRIPLE),
            Some(2_640),
            Some(1_320),
        ),
        ("a file of one work", place(0, 0, 1_320), Some(0), None),
        ("a double with no duration", place(2, 2, 0), None, None),
    ];
    for (case, place, start, runs) in cases {
        assert_eq!((place.start(), place.runs()), (start, runs), "{case}");
    }
}
