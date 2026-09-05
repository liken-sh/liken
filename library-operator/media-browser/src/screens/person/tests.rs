// The person's page over an invented catalog: the words it builds
// out of an entry, the wall of works it reads, and what a select on one
// of them opens.

use std::collections::HashMap;
use std::path::PathBuf;

use super::*;
use crate::catalog::Person as Entry;
use crate::catalog::{
    Answer, Credits, Episode, FileFacts, Franchise, GenreEntry, LibraryEntry, Membership,
    MovieDetails, MovieSet, PlayItem, Selection, SeriesDetails, Slot,
};
use crate::harness::Waker;
use crate::posters::Art;
use crate::screens::{Screen, Step};
use crate::views::Card;

const LIBRARY: &str = "screening/films";
const PATH: &str = ".contributors/A Player";

// A catalog of one person credited in two movies and one series,
// so a wall holds both kinds and a select opens each.
#[derive(Default)]
struct People {
    // Whether the person's entry holds a biography file.
    biography: bool,
    // Whether the person's entry left the store, which is what a
    // re-read finds after the scanner dropped them.
    gone: bool,
    // How many works the person is credited in.
    works: usize,
    // Whether the person is credited as an actor alone, so every work's
    // parts line holds one `as` run and no role word.
    acting: bool,
    // Whether the person is credited as a director alone, so the credit
    // leaves no part behind on any card.
    directing: bool,
    // Whether every work carries a title too long for a cell's band.
    wordy: bool,
}

impl People {
    fn work(&self, number: usize) -> Slot {
        let series = number == 3;
        Slot {
            library: LIBRARY.to_string(),
            kind: match series {
                true => "series".into(),
                false => "movies".into(),
            },
            id: format!("title:{number}"),
            title: match self.wordy {
                true => "W".repeat(60),
                false => format!("Title {number}"),
            },
            released: format!("{}-05-02", 2000 - number),
            art: format!("{number}.jpg"),
            parts: match (self.acting, self.directing) {
                (true, _) => "as The Part".into(),
                (_, true) => "Director".into(),
                _ => "Director, as The Part".into(),
            },
            ..Slot::default()
        }
    }
}

impl Source for People {
    fn franchises(&mut self) -> Vec<crate::catalog::FranchiseEntry> {
        Vec::new()
    }

    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        Vec::new()
    }

    fn franchise(&mut self, _library: &str, _id: &str) -> Option<Franchise> {
        None
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        Vec::new()
    }

    fn wall(&mut self, query: &Query) -> Answer {
        let Query::Person { path, .. } = query else {
            return Answer::default();
        };
        if path != PATH || self.gone {
            return Answer::default();
        }
        Answer {
            name: "A Player".into(),
            slots: (1..=self.works).map(|number| self.work(number)).collect(),
        }
    }

    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        (id == "title:1" || id == "title:2").then(|| MovieDetails {
            title: format!("Film {id}"),
            backdrop: format!("{id}.backdrop.jpg"),
            ..MovieDetails::default()
        })
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        (id == "title:3").then(|| SeriesDetails {
            title: "The Serial".into(),
            backdrop: "serial.backdrop.jpg".into(),
            ..SeriesDetails::default()
        })
    }

    fn episodes(&mut self, _library: &str, _series: &str) -> Vec<Episode> {
        Vec::new()
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        None
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        Credits::default()
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Entry> {
        if path != PATH || self.gone {
            return None;
        }
        Some(Entry {
            library: library.to_string(),
            path: path.to_string(),
            name: "A Player".into(),
            born: "1956-01-02".into(),
            died: "2019-11-30".into(),
            biography: self.biography,
            headshot: true,
            biography_library: match self.biography {
                true => library.to_string(),
                false => String::new(),
            },
            biography_path: path.to_string(),
            headshot_library: library.to_string(),
            headshot_path: path.to_string(),
        })
    }

    fn files(&mut self, _library: &str, _item: &str) -> Vec<FileFacts> {
        Vec::new()
    }

    fn pool(&mut self) -> Vec<crate::catalog::pool::Candidate> {
        Vec::new()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

fn page(people: People) -> (Person, People) {
    let mut source = people;
    let page = Person::open(LIBRARY, PATH, &mut source).expect("the library holds this person");
    (page, source)
}

fn credited(works: usize) -> (Person, People) {
    page(People {
        works,
        ..People::default()
    })
}

#[test]
fn a_page_carries_the_name_the_dates_and_the_files() {
    let (page, _) = credited(3);
    assert_eq!(page.name, "A Player");
    assert_eq!(page.dates, "1956 to 2019");
    assert_eq!(page.headshot, ".contributors/A Player/headshot.jpg");
    assert_eq!(page.headshot_library, LIBRARY);
    assert_eq!(page.biography_path, "");
    assert_eq!(page.works.focus, 0);
}

#[test]
fn a_person_the_library_does_not_hold_has_no_page() {
    let mut source = People::default();
    assert!(Person::open(LIBRARY, ".contributors/Nobody", &mut source).is_none());
}

#[test]
fn the_dates_read_as_the_entry_holds_them() {
    assert_eq!(dates("1956-01-02", "2019-11-30"), "1956 to 2019");
    assert_eq!(dates("1956", ""), "born 1956");
    assert_eq!(dates("", "2019"), "died 2019");
    assert_eq!(dates("", ""), "");
}

#[test]
fn a_work_carries_its_title_and_its_parts() {
    let (page, _) = credited(3);
    let works = &page.works.items;
    assert_eq!(works.len(), 3);
    assert_eq!(works[0].caption(), "Title 1");
    assert_eq!(works[0].line_fitting(80), "Title 1 · 1999");
    assert_eq!(works[0].under(), "Director, as The Part · 1999");
    assert_eq!(works[0].art(), "1.jpg");
    assert_eq!(works[0].name(), "Title 1");
    assert_eq!(works[0].library(), LIBRARY);
    assert_eq!(page.works.heading(), "A Player");
}

#[test]
fn a_work_of_an_actor_alone_leads_with_the_character() {
    let (page, _) = page(People {
        works: 3,
        acting: true,
        ..People::default()
    });
    let works = &page.works.items;
    assert_eq!(works[0].caption(), "The Part");
    assert_eq!(works[0].under(), "Title 1 · 1999");
    assert_eq!(works[0].name(), "Title 1");
}

#[test]
fn a_work_the_credit_leaves_no_part_on_reads_its_kind_and_its_year() {
    let (page, _) = page(People {
        works: 3,
        directing: true,
        ..People::default()
    });
    let works = &page.works.items;
    assert_eq!(works[0].caption(), "Title 1");
    assert_eq!(works[0].under(), "Film · 1999");
    assert_eq!(works[2].caption(), "Title 3");
    assert_eq!(works[2].under(), "Series · 1997");
}

#[test]
fn a_wall_of_works_cuts_both_lines_of_every_card_at_the_read() {
    let (page, _) = page(People {
        works: 3,
        wordy: true,
        ..People::default()
    });
    let band = crate::views::wall::band(crate::views::wall::COLUMNS);
    let card = &page.works.items[0];
    assert!(card.fitted().ends_with('\u{2026}'));
    assert!(card.fitted().chars().count() < card.caption().chars().count());
    assert!(crate::views::text::measured(card.fitted(), crate::look::CAPTION) <= band);
    assert!(crate::views::text::measured(card.under_fitted(), crate::look::FACE) <= band);
}

#[test]
fn the_arrows_move_across_the_wall_of_works() {
    let (mut page, mut source) = credited(3);
    page.key("right", &mut source);
    assert_eq!(page.works.focus, 1);
    page.key("down", &mut source);
    assert_eq!(page.works.focus, 1);
    page.key("right", &mut source);
    page.key("right", &mut source);
    assert_eq!(page.works.focus, 2);
    page.key("left", &mut source);
    assert_eq!(page.works.focus, 1);
}

#[test]
fn a_select_opens_the_movie_the_slot_names() {
    let (mut page, mut source) = credited(3);
    let Step::Open(Screen::Movie(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a movie opens its page");
    };
    assert_eq!(opened.id, "title:1");
}

#[test]
fn a_select_opens_the_series_the_slot_names() {
    let (mut page, mut source) = credited(3);
    page.works.focus = 2;
    let Step::Open(Screen::Series(opened)) = page.key("enter", &mut source) else {
        panic!("a select on a series opens its page");
    };
    assert_eq!(opened.id, "title:3");
}

#[test]
fn a_page_with_no_works_opens_nothing() {
    let (mut page, mut source) = credited(0);
    assert!(page.works.items.is_empty());
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert!(matches!(page.key("right", &mut source), Step::Stay));
    assert_eq!(page.works.focus, 0);
}

#[test]
fn the_focused_work_names_the_backdrop_the_store_decodes_next() {
    let (page, mut source) = credited(3);
    assert_eq!(
        page.resting(&mut source),
        Some((LIBRARY.to_string(), "title:1.backdrop.jpg".to_string()))
    );
}

#[test]
fn a_reread_that_shortens_the_wall_clamps_the_focus() {
    let (mut page, mut source) = credited(3);
    page.works.focus = 2;
    source.works = 1;
    page.reread(&mut source);
    assert_eq!(page.works.items.len(), 1);
    assert_eq!(page.works.focus, 0);
}

#[test]
fn a_reread_of_a_person_that_left_the_store_keeps_the_page() {
    let (mut page, mut source) = credited(3);
    source.gone = true;
    page.reread(&mut source);
    assert_eq!(page.works.items.len(), 3);
}

// A store over one volume, so a test reads the biography the way
// the browser does.
#[derive(Default)]
struct Volume {
    roots: HashMap<String, PathBuf>,
}

impl Posters for Volume {
    fn poster(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Art> {
        None
    }

    fn file(&self, library: &str, path: &str) -> Option<PathBuf> {
        if path.contains("..") {
            return None;
        }
        Some(self.roots.get(library)?.join(path))
    }
}

#[test]
fn the_biography_arrives_from_the_file_beside_the_entry() {
    let dir = tempfile::TempDir::new().expect("a scratch volume");
    let entry = dir.path().join(PATH);
    std::fs::create_dir_all(&entry).expect("the entry's directory");
    std::fs::write(entry.join("biography.txt"), "One line of a life.").expect("the file");

    let (mut page, _) = page(People {
        biography: true,
        works: 1,
        ..People::default()
    });
    assert_eq!(page.biography_path, ".contributors/A Player/biography.txt");

    let store = Volume {
        roots: HashMap::from([(LIBRARY.to_string(), dir.path().to_path_buf())]),
    };
    page.read_biography(&store);
    assert_eq!(page.biography, "One line of a life.");
}

#[test]
fn a_person_with_no_biography_file_reads_nothing() {
    let (mut page, _) = credited(1);
    page.biography = "stale".into();
    page.read_biography(&Volume::default());
    assert_eq!(page.biography, "");
}
