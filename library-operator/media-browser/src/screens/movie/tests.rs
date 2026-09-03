// The movie page over an invented catalog: the words it builds out of a
// body, and where a press takes focus.

use super::*;
use crate::catalog::{
    Credit, CreditSlot, Credits, Episode, LibraryEntry, Person, PlayItem, SeriesDetails, Title,
    Work,
};
use crate::harness::Waker;

// A catalog of one set of three films, so a page has siblings to move
// across.
#[derive(Default)]
struct Films {
    trailer: bool,
    set: bool,
    // Whether the movie credits anybody at all, and whether the
    // one player it credits has an entry in the contributor store.
    credits: bool,
    stranger: bool,
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

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        if !self.credits {
            return Credits::default();
        }
        Credits {
            directors: vec![slot("A Director", "", true)],
            writers: Vec::new(),
            cast: vec![
                slot("A Player", "The Part", !self.stranger),
                slot("Another", "A Walk-on", true),
            ],
        }
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Person> {
        Some(Person {
            library: library.to_string(),
            path: path.to_string(),
            name: path.rsplit('/').next()?.to_string(),
            ..Person::default()
        })
    }

    fn works(&mut self, _library: &str, _path: &str) -> Vec<Work> {
        Vec::new()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

// One credit of the invented title, with or without an entry in
// the contributor store.
fn slot(name: &str, role: &str, entry: bool) -> CreditSlot {
    CreditSlot {
        name: name.to_string(),
        role: role.to_string(),
        contributor: match entry {
            true => format!(".contributors/{name}"),
            false => String::new(),
        },
        headshot: entry,
    }
}

// A page whose title credits a director and two players.
fn credited() -> (Movie, Films) {
    page(Films {
        credits: true,
        ..Films::default()
    })
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
fn a_page_builds_its_facts_and_its_title() {
    let (page, _) = page(Films::default());
    assert_eq!(page.facts, "1994 · 1h 52m · PG · Drama");
    assert_eq!(page.title, "Film two");
}

#[test]
fn a_page_draws_a_stripe_for_every_part_the_title_credits() {
    let (page, _) = credited();
    let headings: Vec<&str> = page
        .stripes
        .bands()
        .iter()
        .map(|band| band.heading)
        .collect();
    assert_eq!(headings, ["Crew", "Cast"]);
    assert_eq!(page.stripes.bands()[1].faces.len(), 2);
}

#[test]
fn a_title_that_credits_nobody_draws_no_stripe() {
    let (page, _) = page(Films::default());
    assert!(page.stripes.is_empty());
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
fn down_with_no_strip_and_no_stripe_moves_nowhere() {
    let (mut page, mut source) = page(Films::default());
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn down_from_the_buttons_reaches_the_stripes_where_the_movie_is_in_no_set() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn down_from_the_strip_reaches_the_stripes_and_up_returns_to_it() {
    let (mut page, mut source) = page(Films {
        set: true,
        credits: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
}

#[test]
fn the_arrows_move_within_a_stripe_and_between_the_stripes() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 1));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
}

#[test]
fn a_select_on_a_headshot_opens_the_persons_page() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);

    let Step::Open(Screen::Person(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a headshot opens the person");
    };
    assert_eq!(opened.path, ".contributors/A Director");
    assert_eq!(opened.name, "A Director");
}

#[test]
fn a_select_on_a_name_with_no_entry_opens_nothing() {
    let (mut page, mut source) = page(Films {
        credits: true,
        stranger: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    page.key("down", &mut source);

    assert_eq!(page.focus, Focus::Stripe(1, 0));
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_keeps_the_rung_a_stripe_holds() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
    page.key("down", &mut source);
    page.key("right", &mut source);

    page.reread(&mut source);

    assert_eq!(page.focus, Focus::Stripe(1, 1));
}

#[test]
fn a_page_whose_credits_left_holds_focus_on_play() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
    source.credits = false;

    page.reread(&mut source);

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
