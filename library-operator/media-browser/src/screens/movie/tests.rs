// The movie page over an invented catalog: the words it builds out of a
// body, and where a press takes focus.

use super::*;
use crate::catalog::Answer;
use crate::catalog::franchise::{Entry, Held, MOVIE};
use crate::catalog::{
    Credit, CreditSlot, Credits, Episode, FileFacts, Franchise, GenreEntry, LibraryEntry,
    Membership, Person, PlayItem, Query, SeriesDetails, Title,
};

// The Library of kind franchises the fake holds, and the one franchise
// in it.
const ORDERS: &str = "screening/orders";
const CYCLE: &str = "franchise:name:the-cycle";

use crate::harness::Waker;
use crate::screens::franchise::strips::Place;

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
    // The set row is there but every member has left it, which a page
    // reads between the scanner's member deletes and its set delete.
    emptied: bool,
    // Whether the movie belongs to a franchise, which puts a strip
    // under the set strip.
    franchise: bool,
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
            studios: vec!["A Studio".into(), "Another Studio".into()],
            ratings: vec![
                ("imdb".into(), 6.5),
                ("metacritic".into(), 80.0),
                ("themoviedb".into(), 7.1),
                ("tomatometerallcritics".into(), 83.0),
            ],
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
    fn franchises(&mut self) -> Vec<crate::catalog::FranchiseEntry> {
        Vec::new()
    }

    // One franchise of the same three films, which is what puts a
    // franchise strip on the page.
    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        if !self.franchise {
            return Vec::new();
        }
        vec![Membership {
            movies: 3,
            series: 0,
            library: ORDERS.into(),
            id: CYCLE.into(),
            title: "The Cycle".into(),
            members: ["one", "two", "three"]
                .iter()
                .enumerate()
                .map(|(position, id)| Entry {
                    position: position as i64,
                    kind: MOVIE.into(),
                    alias: format!("movie:tmdb:{id}"),
                    title: format!("Film {id} (1994)"),
                    held: Some(Held {
                        arts: Vec::new(),
                        library: "screening/films".into(),
                        id: (*id).to_string(),
                        kind: "movies".into(),
                        title: format!("Film {id}"),
                        art: format!("{id}.jpg"),
                        released: "1994".into(),
                        slug: (*id).to_string(),
                        tagline: String::new(),
                        plot: String::new(),
                        duration: 0,
                    }),
                    ..Entry::default()
                })
                .collect(),
        }]
    }

    fn franchise(&mut self, library: &str, id: &str) -> Option<Franchise> {
        if !self.franchise || library != ORDERS || id != CYCLE {
            return None;
        }
        Some(Franchise {
            library: library.to_string(),
            id: id.to_string(),
            title: "The Cycle".into(),
            entries: self.franchises_of("", "").remove(0).members,
            ..Franchise::default()
        })
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        Vec::new()
    }

    fn wall(&mut self, _query: &Query) -> Answer {
        Answer::default()
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
        if self.emptied {
            return Some(MovieSet {
                title: "The Set".into(),
                members: Vec::new(),
            });
        }
        Some(MovieSet {
            title: "The Set".into(),
            members: ["one", "two", "three"]
                .iter()
                .map(|id| Title {
                    id: (*id).to_string(),
                    title: format!("Film {id}"),
                    released: "2019-05-04".into(),
                    duration: 8_460,
                    rating: "PG-13".into(),
                    // The first member carries a tagline and the others
                    // do not, so the strip draws both kinds of words a
                    // card leads with.
                    tagline: match *id == "one" {
                        true => "One line of it.".to_string(),
                        false => String::new(),
                    },
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

    fn files(&mut self, _library: &str, item: &str) -> Vec<FileFacts> {
        if item != "two" {
            return Vec::new();
        }
        vec![
            FileFacts {
                role: "primary".into(),
                kind: "video".into(),
                video_codec: "x265".into(),
                audio_codec: "AC3".into(),
                width: 1_920,
                height: 804,
                size_bytes: 4_200_000_000,
                ..FileFacts::default()
            },
            FileFacts {
                role: "subtitle".into(),
                kind: "subtitle".into(),
                language: "en".into(),
                ..FileFacts::default()
            },
        ]
    }

    fn pool(&mut self) -> Vec<crate::catalog::pool::Candidate> {
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

// A page whose movie is in a set and in a franchise, which is the
// page with every strip on it.
fn ordered() -> (Movie, Films) {
    page(Films {
        set: true,
        franchise: true,
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
    assert_eq!(page.facts, "May 2, 1994 · 1h 52m · PG · Drama");
    assert_eq!(page.title, "Film two");
}

#[test]
fn a_page_carries_the_scores_its_body_holds_and_leaves_tmdb_off() {
    let (page, _) = page(Films::default());
    let marks: Vec<ratings::Mark> = page.ratings.iter().map(|score| score.mark).collect();
    assert_eq!(
        marks,
        [
            ratings::Mark::Imdb,
            ratings::Mark::Tomato,
            ratings::Mark::Metacritic
        ]
    );
}

#[test]
fn the_foot_names_the_studios_and_the_files_the_movie_holds() {
    let (page, _) = page(Films::default());
    let rows: Vec<String> = page
        .foot
        .rows()
        .map(|row| row.content.to_string())
        .collect();
    assert_eq!(
        rows,
        [
            "A Studio, Another Studio",
            "1920×804 · x265 · AC3 · 4.2 GB · Subtitles: English",
        ]
    );
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
    assert_eq!(headings, ["Cast", "Crew"]);
    assert_eq!(page.stripes.bands()[0].faces.len(), 2);
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
    assert_eq!(set.heading, "The Set · a 3-film set");
    let ids: Vec<&str> = set
        .members
        .iter()
        .map(|member| member.id.as_str())
        .collect();
    assert_eq!(ids, ["one", "two", "three"]);
    assert_eq!(set.current, 1);
}

#[test]
fn a_set_strip_draws_a_card_of_two_lines_over_the_year_and_the_runtime() {
    let (page, _) = page(Films {
        set: true,
        ..Films::default()
    });
    let set = page.set.expect("the movie belongs to a set");
    assert_eq!(set.members[0].fitted, "One line of it.");
    assert!(set.members[0].tagline);
    assert_eq!(set.members[1].fitted, "Film two");
    assert!(!set.members[1].tagline);
    for member in &set.members {
        assert_eq!(member.under_fitted, "2019 · 2h 21m");
    }
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
    assert_eq!(page.focus, Focus::Stripe(0, 0));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(1, 0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
}

#[test]
fn a_select_on_a_headshot_opens_the_persons_page() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
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

    assert_eq!(page.focus, Focus::Stripe(0, 0));
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_keeps_the_rung_a_stripe_holds() {
    let (mut page, mut source) = credited();
    page.key("down", &mut source);
    page.key("right", &mut source);

    page.reread(&mut source);

    assert_eq!(page.focus, Focus::Stripe(0, 1));
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
fn a_select_past_the_set_strips_last_member_opens_nothing() {
    let mut source = Films {
        set: true,
        ..Films::default()
    };
    let mut page = Movie::open("screening/films", "one", &mut source).expect("the film is there");
    page.focus = Focus::Strip(9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_select_on_a_stripe_slot_past_the_credits_opens_nothing() {
    let (mut page, mut source) = credited();
    page.focus = Focus::Stripe(9, 9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert_eq!(page.focus, Focus::Stripe(9, 9));
}

#[test]
fn down_from_the_set_strip_holds_where_nothing_stands_under_it() {
    let mut source = Films {
        set: true,
        ..Films::default()
    };
    let mut page = Movie::open("screening/films", "one", &mut source).expect("the film is there");
    page.focus = Focus::Strip(1);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
}

#[test]
fn down_from_a_franchise_member_holds_where_the_film_credits_nobody() {
    let mut source = Films {
        franchise: true,
        ..Films::default()
    };
    let mut page = Movie::open("screening/films", "one", &mut source).expect("the film is there");
    page.focus = Focus::Franchise(0, Place::Member(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(1)));
}

#[test]
fn a_select_on_a_franchise_heading_past_the_strips_opens_nothing() {
    let mut source = Films {
        franchise: true,
        ..Films::default()
    };
    let mut page = Movie::open("screening/films", "one", &mut source).expect("the film is there");
    page.focus = Focus::Franchise(9, Place::Heading);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    page.focus = Focus::Franchise(9, Place::Member(0));
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_that_finds_the_set_emptied_lands_focus_on_the_buttons() {
    let mut source = Films {
        set: true,
        ..Films::default()
    };
    let mut page = Movie::open("screening/films", "one", &mut source).expect("the film is there");
    page.focus = Focus::Strip(1);
    source.emptied = true;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
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

#[test]
fn a_set_heading_says_how_many_films_the_set_holds() {
    assert_eq!(films(2), "a 2-film set");
    assert_eq!(films(3), "a 3-film set");
}

#[test]
fn a_movie_in_a_franchise_draws_a_strip_under_its_set_strip() {
    let (ordered, _) = ordered();
    assert_eq!(ordered.franchises.bands().len(), 1);
    assert_eq!(
        ordered.franchises.bands()[0].heading,
        "The Cycle · a franchise of 3 films"
    );
    assert_eq!(ordered.franchises.bands()[0].current, Some(1));
    let (plain, _) = page(Films::default());
    assert!(plain.franchises.is_empty());
}

#[test]
fn down_from_the_set_strip_reaches_the_franchise_strips_heading() {
    let (mut page, mut source) = ordered();
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(1)));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Stripe(0, 0));
}

#[test]
fn up_from_the_stripes_climbs_back_through_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Stripe(0, 0);
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(1)));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Strip(1));
}

#[test]
fn a_movie_in_a_franchise_and_no_set_reaches_the_strip_from_the_buttons() {
    let (mut page, mut source) = page(Films {
        franchise: true,
        ..Films::default()
    });
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Heading));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn left_and_right_move_across_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(2)));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(1)));
}

#[test]
fn a_press_on_the_strips_heading_opens_the_franchises_page() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Heading);
    let step = page.key("enter", &mut source);
    assert!(matches!(step, Step::Open(Screen::Franchise(_))));
}

#[test]
fn a_press_on_a_member_replaces_this_page_with_that_members() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(2));
    let step = page.key("enter", &mut source);
    assert!(matches!(step, Step::Replace(Screen::Movie(_))));
}

#[test]
fn a_press_on_a_member_no_library_holds_opens_nothing() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(9));
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    page.focus = Focus::Franchise(9, Place::Heading);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_holds_the_rung_of_the_franchise_strip() {
    let (mut page, mut source) = ordered();
    page.focus = Focus::Franchise(0, Place::Member(2));
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(2)));

    page.focus = Focus::Franchise(0, Place::Member(9));
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Franchise(0, Place::Member(2)));

    source.franchise = false;
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Buttons(0));
}
