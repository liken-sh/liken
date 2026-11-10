// The status line of a movie page with one person and with two at the
// screen: the words for the whole audience, the month a screen play
// finished the film, and a face for each person with where that person
// stands, read from the answer the posters read.

use super::super::*;
use super::Films;
use super::resume::own;
use crate::catalog::progress::{Origin, seen};
use crate::catalog::{Progress, Reach, Resume, Seen};

// The middle of March 2026, so the month is the same in every zone.
const MARCH: i64 = 1_773_662_400;

// The film the page is about, as the store's answers key it.
fn film() -> (String, String) {
    ("screening/films".to_string(), "two".to_string())
}

fn viewer(name: &str) -> Viewer {
    Viewer {
        name: name.to_string(),
        letter: name[..1].to_uppercase(),
    }
}

// A play of the film that reached its end, from this origin.
fn finished(origin: Origin, recorded: i64) -> Resume {
    own(
        Progress {
            play: format!("play-{recorded}"),
            position: 6_000,
            duration: 6_000,
            finished: true,
            recorded,
            origin,
            ..Progress::default()
        },
        true,
    )
}

// The page after the browser read the plays of the film for these people.
fn read(source: Films, people: &[&str]) -> Movie {
    let mut source = Films {
        trailer: true,
        ..source
    };
    let mut page =
        Movie::open("screening/films", "two", &mut source).expect("the catalog holds this movie");
    let names: Vec<String> = people.iter().map(|name| name.to_string()).collect();
    let viewers: Vec<Viewer> = people.iter().map(|name| viewer(name)).collect();
    page.read_progress(&mut source, &names, &viewers);
    page
}

#[test]
fn one_person_reads_the_month_a_screen_play_finished_the_film() {
    let cases = [
        (
            vec![finished(Origin::Screen, MARCH)],
            "Watched · March 2026",
        ),
        (
            vec![
                finished(Origin::Screen, MARCH),
                finished(Origin::Outside, MARCH + 60),
            ],
            "Watched · March 2026",
        ),
        (vec![finished(Origin::Mark, MARCH)], "Marked watched"),
        (vec![finished(Origin::Outside, MARCH)], "Watched"),
    ];
    for (plays, words) in cases {
        let page = read(
            Films {
                plays,
                ..Films::default()
            },
            &["first"],
        );
        assert_eq!(page.status().words, words);
        assert!(page.people.is_empty());
    }
}

// The first person finished the film and the second stopped part way, so
// the audience as a whole has not started it, and the faces say why.
#[test]
fn two_people_read_the_audience_together_and_each_person_beside_a_face() {
    let reach = Reach {
        people: vec![("first".to_string(), 1.0), ("second".to_string(), 0.414)],
        finishes: vec![(
            "first".to_string(),
            crate::catalog::progress::Finish::Played { at: MARCH },
        )],
    };
    let page = read(
        Films {
            reach: [(film(), reach)].into_iter().collect(),
            ..Films::default()
        },
        &["first", "second", "third"],
    );

    assert!(page.status().words.starts_with("Not started together"));
    assert_eq!(
        page.people,
        [
            (viewer("first"), "watched".to_string()),
            (viewer("second"), "41%".to_string()),
            (viewer("third"), "not started".to_string()),
        ]
    );
}

// One night in the store: the people on the play, the second it reached
// of the film's 6,720, and when it was recorded.
type Night = (&'static [&'static str], i64, i64);

// The page for this audience after these nights. The fake answers the
// plays and the reach that the store's rules give for this audience, so
// the line and the faces read what a real read gives them.
fn after(nights: &[Night], audience: &[&str]) -> Movie {
    let (library, id) = film();
    let seen: Vec<Seen> = nights
        .iter()
        .map(|(people, position, recorded)| Seen {
            resume: Resume {
                library: library.clone(),
                kind: "movie".to_string(),
                id: id.clone(),
                progress: Progress {
                    play: format!("play-{recorded}"),
                    position: *position,
                    duration: 6_720,
                    finished: *position >= 6_500,
                    recorded: *recorded,
                    origin: Origin::Screen,
                    ..Progress::default()
                },
                ..Resume::default()
            },
            people: people.iter().map(|name| name.to_string()).collect(),
        })
        .collect();
    let names: Vec<String> = audience.iter().map(|name| name.to_string()).collect();
    read(
        Films {
            plays: seen::every(&seen, &names, Some((&library, &id))),
            reach: seen::films(&seen, &names),
            ..Films::default()
        },
        audience,
    )
}

const PAIR: &[&str] = &["ann", "ben"];
const CROWD: &[&str] = &["ann", "ben", "cal"];
const ANN: &[&str] = &["ann"];
const BEN: &[&str] = &["ben"];
const ANN_AND_CAL: &[&str] = &["ann", "cal"];

// The audience is Ann and Ben. A play counts for the two of them together
// when it names both, whoever else it names, and the newest such play is
// where they stand, so a film they finished and then started again reads
// where the second play stopped. A play without one of them, and two plays
// that each finished the film alone, leave them not started together while
// each face says how far that person got.
#[test]
fn the_line_reads_the_newest_play_that_names_everyone_at_the_screen() {
    let cases: [(&[Night], &str, [&str; 2]); 7] = [
        (
            &[(PAIR, 6_720, MARCH)],
            "Watched together · March 2026",
            ["watched", "watched"],
        ),
        (
            &[(CROWD, 6_720, MARCH)],
            "Watched together · March 2026",
            ["watched", "watched"],
        ),
        (
            &[(CROWD, 2_912, MARCH)],
            "0:48:32 watched together · 1h 3m remaining",
            ["43%", "43%"],
        ),
        (
            &[(ANN_AND_CAL, 6_720, MARCH)],
            "Not started together · 1h 52m",
            ["watched", "not started"],
        ),
        (
            &[(ANN, 6_720, MARCH), (BEN, 6_720, MARCH + 60)],
            "Not started together · 1h 52m",
            ["watched", "watched"],
        ),
        (
            &[(PAIR, 6_720, MARCH), (PAIR, 2_912, MARCH + 60)],
            "0:48:32 watched together · 1h 3m remaining",
            ["watched", "watched"],
        ),
        (
            &[(PAIR, 6_720, MARCH), (CROWD, 2_912, MARCH + 60)],
            "0:48:32 watched together · 1h 3m remaining",
            ["watched", "watched"],
        ),
    ];
    for (nights, words, faces) in cases {
        let page = after(nights, PAIR);
        assert_eq!(page.status().words, words, "{nights:?}");
        let read: Vec<&str> = page.people.iter().map(|(_, word)| word.as_str()).collect();
        assert_eq!(read, faces, "{nights:?}");
    }
}

// The marks belong to the line, so a film the line says the two finished
// offers the clear, and one it says they are in the middle of offers both.
#[test]
fn the_marks_follow_the_line() {
    let cases: [(&[Night], &[watch::Mark]); 3] = [
        (&[(CROWD, 6_720, MARCH)], &[watch::Mark::Cleared]),
        (
            &[(CROWD, 2_912, MARCH)],
            &[watch::Mark::Watched, watch::Mark::Cleared],
        ),
        (&[(ANN_AND_CAL, 6_720, MARCH)], &[watch::Mark::Watched]),
    ];
    for (nights, marks) in cases {
        assert_eq!(after(nights, PAIR).marks(), marks, "{nights:?}");
    }
}

// The buttons resume where the continue-watching row's thread stands, and
// a thread starts only at a play that names exactly the people at the
// screen. So a film the two are in the middle of with a third person reads
// as resumed on the line while its row offers Play from the beginning.
#[test]
fn the_buttons_keep_the_thread_rule_of_the_continue_watching_row() {
    let page = after(&[(CROWD, 2_912, MARCH)], PAIR);

    assert_eq!(super::words(&page), ["Play", "Trailer"]);
}

// One person at the screen keeps the thread rule of the continue-watching
// row: a play that named someone more counts only after a play of their
// own, so a film Ann watched only with Ben reads not started to Ann alone.
#[test]
fn one_person_reads_the_line_by_the_thread_rule() {
    let page = after(&[(PAIR, 6_720, MARCH)], ANN);

    assert_eq!(page.status().words, "Not started · 1h 52m");
    assert!(page.people.is_empty());
}
