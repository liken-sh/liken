// The status line of a movie page with one person and with two at the
// screen: the words for the whole audience, the month a screen play
// finished the film, and a face for each person with where that person
// stands, read from the answer the posters read.

use super::super::*;
use super::Films;
use super::resume::own;
use crate::catalog::progress::Origin;
use crate::catalog::{Progress, Reach, Resume};

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
