// The movie page over an invented catalog: the words it builds out of a
// body, and where a press takes focus.

use super::*;
use crate::catalog::{Credit, Episode, LibraryEntry, PlayItem, SeriesDetails, Title};
use crate::harness::Waker;

// A catalog of one set of three films, so a page has siblings to move
// across.
#[derive(Default)]
struct Films {
    trailer: bool,
    set: bool,
    // The movie names a set that no sets row holds, which is what a page
    // reads between the scanner's movie write and its set write.
    lost: bool,
}

impl Films {
    fn details(&self, id: &str) -> MovieDetails {
        MovieDetails {
            title: format!("Film {id}"),
            released: "1994-05-02".into(),
            duration: 6_720,
            rating: "PG".into(),
            genres: vec!["Drama".into()],
            tagline: "One line of it.".into(),
            plot: "A plot.".into(),
            directors: vec!["A Director".into()],
            writers: vec!["A Writer".into(), "Another".into()],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            set_id: if self.set {
                "set:one".into()
            } else {
                String::new()
            },
            backdrop: format!("{id}.backdrop.jpg"),
            logo: String::new(),
            trailer: if self.trailer {
                format!("{id}.trailer.mkv")
            } else {
                String::new()
            },
        }
    }
}

impl Source for Films {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn titles(&mut self, _library: &str, _kind: &str) -> Vec<Title> {
        Vec::new()
    }

    fn series(&mut self, _library: &str, _id: &str) -> Option<SeriesDetails> {
        None
    }

    fn episodes(&mut self, _library: &str, _series: &str) -> Vec<Episode> {
        Vec::new()
    }

    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        ["one", "two", "three"]
            .contains(&id)
            .then(|| self.details(id))
    }

    fn set(&mut self, _library: &str, id: &str) -> Option<MovieSet> {
        if id != "set:one" || self.lost {
            return None;
        }
        Some(MovieSet {
            title: "The Set".into(),
            members: ["one", "two", "three"]
                .iter()
                .map(|id| Title {
                    id: (*id).to_string(),
                    title: format!("Film {id}"),
                    ..Title::default()
                })
                .collect(),
        })
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

fn page(films: Films) -> (Movie, Films) {
    let mut source = films;
    let page =
        Movie::open("screening/films", "two", &mut source).expect("the catalog holds this movie");
    (page, source)
}

#[test]
fn a_page_opens_with_focus_on_play() {
    let (page, _) = page(Films::default());
    assert_eq!(page.focus, Focus::Buttons(0));
    assert_eq!(page.buttons(), ["Play"]);
}

#[test]
fn a_movie_with_a_trailer_file_gets_the_second_button() {
    let (page, _) = page(Films {
        trailer: true,
        ..Films::default()
    });
    assert_eq!(page.buttons(), ["Play", "Trailer"]);
}

#[test]
fn a_page_builds_its_facts_its_credits_and_its_cast() {
    let (page, _) = page(Films::default());
    assert_eq!(page.facts, "1994 · 1h 52m · PG · Drama");
    assert_eq!(page.directed, "Directed by A Director");
    assert_eq!(page.written, "Written by A Writer, Another");
    assert_eq!(page.cast, "A Player as The Part");
    assert_eq!(page.title, "Film two");
}

#[test]
fn a_page_of_a_movie_the_catalog_does_not_hold_opens_nothing() {
    let mut source = Films::default();
    assert!(Movie::open("screening/films", "gone", &mut source).is_none());
}

#[test]
fn a_movie_in_no_set_draws_no_strip() {
    let (page, _) = page(Films::default());
    assert!(page.set.is_none());
}

#[test]
fn a_set_strip_holds_the_whole_set_and_marks_this_film() {
    let (page, _) = page(Films {
        set: true,
        ..Films::default()
    });
    let set = page.set.expect("the movie belongs to a set");
    assert_eq!(set.heading, "The Set");
    let ids: Vec<&str> = set
        .members
        .iter()
        .map(|member| member.id.as_str())
        .collect();
    assert_eq!(ids, ["one", "two", "three"]);
    assert_eq!(set.current, 1);
}

#[test]
fn left_and_right_move_across_the_buttons() {
    let (mut page, mut source) = page(Films {
        trailer: true,
        ..Films::default()
    });
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Buttons(1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Buttons(1));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn play_and_trailer_ask_for_their_own_choices() {
    let (mut page, mut source) = page(Films {
        trailer: true,
        ..Films::default()
    });
    assert!(matches!(
        page.key("enter", &mut source),
        Step::Play {
            selection: Selection::Movie { id },
            ..
        } if id == "two"
    ));
    page.key("right", &mut source);
    assert!(matches!(
        page.key("enter", &mut source),
        Step::Play {
            selection: Selection::Trailer { id },
            ..
        } if id == "two"
    ));
}

#[test]
fn down_reaches_the_strip_at_this_film_and_up_returns_to_play() {
    let (mut page, mut source) = page(Films {
        set: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Strip(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn down_with_no_strip_moves_nowhere() {
    let (mut page, mut source) = page(Films::default());
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn a_select_on_a_sibling_replaces_the_page() {
    let (mut page, mut source) = page(Films {
        set: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    page.key("left", &mut source);

    let step = page.key("enter", &mut source);

    let Step::Replace(Screen::Movie(opened)) = step else {
        panic!("a sibling replaces the page");
    };
    assert_eq!(opened.id, "one");
    assert_eq!(opened.focus, Focus::Buttons(0));
}

#[test]
fn a_select_on_this_film_replaces_nothing() {
    let (mut page, mut source) = page(Films {
        set: true,
        ..Films::default()
    });
    page.key("down", &mut source);

    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_keeps_the_focus_the_page_holds() {
    let (mut page, mut source) = page(Films {
        set: true,
        trailer: true,
        ..Films::default()
    });
    page.key("right", &mut source);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Buttons(1));

    page.key("down", &mut source);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Strip(1));
}

#[test]
fn a_reread_of_a_movie_that_left_keeps_the_page() {
    let (mut page, _) = page(Films::default());
    let mut empty = Films::default();
    page.id = "gone".into();

    page.reread(&mut empty);

    assert_eq!(page.title, "Film two");
}

#[test]
fn a_page_whose_set_left_holds_focus_on_play() {
    let (mut page, mut source) = page(Films {
        set: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    page.set = None;

    page.key("right", &mut source);

    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn a_movie_that_names_a_set_the_catalog_lost_draws_no_strip() {
    let (page, _) = page(Films {
        set: true,
        lost: true,
        ..Films::default()
    });
    assert!(page.set.is_none());
}
