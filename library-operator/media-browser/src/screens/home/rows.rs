// The rows of the home page and the strip that reads one. A row names
// what a strip reads: the slots of one query, the libraries, or the
// genres. The strip holds the read's items, the focus, and what a select
// opens. The page's order is fixed here, and the home page and its read
// both take it from here.

use crate::catalog::pool::Candidate;
use crate::catalog::recency::SHOWN;
use crate::catalog::{
    Fold, FranchiseEntry, GenreEntry, LibraryEntry, Order, Query, Source, library_name,
};
use crate::screens;
use crate::screens::wall::Wall;
use crate::screens::{Item, Screen, Step, credits, facts, franchise, person, slots};
use crate::views::{card, strip};

// The fewest slots a drawn strip shows. A strip the day drew that
// reads fewer holds nothing, so a whole row never stands on two or
// three posters. The recency strips, the libraries, the genres, and the
// franchises show whatever they hold, because they are not a draw.
const FLOOR: usize = 4;

// The kind an item of the libraries strip carries, so a select on it
// opens the library's wall and never a page.
pub(super) const LIBRARY: &str = "library";

// The kind an item of the genres strip carries, so a select on it opens
// the genre's page and never a title's.
pub(super) const GENRE: &str = "genre";

// The kind an item of the franchises strip carries, so a select on it opens
// the franchise's page and never a title's.
pub(super) const FRANCHISE: &str = "franchise";

/// One row of the page as a read: the banner, the slots of one query,
/// the libraries themselves, the genres themselves, or the franchises
/// themselves.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Row {
    Banner,
    Query(Query),
    Libraries,
    Genres,
    Franchises,
}

impl Row {
    // Whether the row is one of the two recency strips. They are told
    // apart because they feed the banner after the drawn strips, in the
    // page's order.
    pub(super) fn recency(&self) -> bool {
        matches!(
            self,
            Self::Query(Query::Released { .. } | Query::Added { .. })
        )
    }
}

// The rows of the page, top to bottom: the banner, the two recency
// strips under the `Shows` fold on `today` in seconds, so a show takes
// one slot however many episodes it holds, the strips the day drew in
// the drawn order, the libraries, the genres, and the franchises to close
// the page.
pub(super) fn rows(today: i64, drawn: Vec<Candidate>) -> Vec<Row> {
    let fold = Fold::Shows { today };
    let mut rows = vec![
        Row::Banner,
        Row::Query(Query::Released { fold }),
        Row::Query(Query::Added { fold }),
    ];
    rows.extend(
        drawn
            .into_iter()
            .map(|candidate| Row::Query(candidate.query)),
    );
    rows.push(Row::Libraries);
    rows.push(Row::Genres);
    rows.push(Row::Franchises);
    rows
}

/// The slot that ends a strip: its words, and the art it draws as with
/// the library that art resolves against, both empty where it draws its
/// words alone.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Last {
    pub words: String,
    pub library: String,
    pub art: String,
}

impl Last {
    // A slot of words alone.
    fn words(words: String) -> Self {
        Self {
            words,
            library: String::new(),
            art: String::new(),
        }
    }

    // The slot about a person: their name in the words, and their
    // headshot as the art, where a library holds one.
    fn about(library: &str, path: &str, name: &str, source: &mut dyn Source) -> Self {
        let (library, art) = source
            .person(library, path)
            .map(|entry| person::headshot(&entry))
            .unwrap_or_default();
        Self {
            words: format!("About {name}"),
            library,
            art,
        }
    }

    /// The slot as the strip view draws it.
    pub fn view(&self) -> strip::Last<'_> {
        strip::Last {
            words: &self.words,
            library: &self.library,
            art: &self.art,
        }
    }
}

/// One strip of the page: the row it reads, the heading over it, the
/// items in the read's order, the focused index, the words on a slot that
/// ends it, or nothing, and the caption lines under each slot.
#[derive(Debug)]
pub struct Strip {
    pub row: Row,
    pub heading: String,
    pub items: Vec<Item>,
    pub focus: usize,
    pub last: Option<Last>,
    pub lines: usize,
}

impl Strip {
    // A strip of this row with nothing read yet. Every strip is built empty
    // and read in the page's order, because the added strip reads what the
    // released strip shows.
    pub(super) fn new(row: Row) -> Self {
        Self {
            heading: String::new(),
            items: Vec::new(),
            focus: 0,
            last: None,
            lines: card::LINES,
            row,
        }
    }

    // Read the strip's row again and keep focus in range. A query strip
    // shows the first `SHOWN` slots and leaves the rest to the wall, and
    // it ends in a "see all" slot only where the read answered more than
    // the strip shows. A person's strip always ends in a slot about them,
    // because their page holds the headshot and the biography and not
    // only the works. The released strip keeps the window of today, and
    // the added strip drops what the released strip shows.
    // A drawn strip under `FLOOR` holds nothing at all, not even its
    // "see all" or "about" slot, so a two-film set the day drew takes
    // no row.
    pub(super) fn reread(&mut self, source: &mut dyn Source, today: i64, released: &[Item]) {
        match &self.row {
            Row::Query(query) => {
                let mut answer = source.wall(query);
                // A person's heading is two-tone: the name bright, and
                // after the dot the person's roles across the strip's
                // works, most frequent first.
                let roles = credits::credit(query, &mut answer.slots);
                self.heading = match query {
                    Query::Person { .. } => facts::joined(&[&answer.name, &roles]),
                    _ => query.name(&answer.name),
                };
                let answered = answer.slots.len();
                let slots = match query {
                    Query::Released { .. } => super::recent::released(answer.slots, today),
                    Query::Added { .. } => super::recent::added(answer.slots, released),
                    _ => answer.slots.into_iter().take(SHOWN).collect(),
                };
                let floored = !self.row.recency() && slots.len() < FLOOR;
                self.last = match (floored, query) {
                    (true, _) => None,
                    (_, Query::Person { library, path }) => {
                        Some(Last::about(library, path, &answer.name, source))
                    }
                    _ => (slots.len() < answered).then(|| Last::words(strip::SEE_ALL.to_string())),
                };
                self.items = match floored {
                    true => Vec::new(),
                    false => slots
                        .into_iter()
                        .map(|slot| Item::of(query, slot))
                        .collect(),
                };
            }
            // The genres row reads every genre. It has no "see all", because
            // it is all of them and not a draw.
            Row::Genres => {
                self.heading = "Genres".to_string();
                self.items = source.genres().into_iter().map(genre_item).collect();
            }
            // The franchises row reads every franchise of the namespace, in
            // sort order, and has no "see all" for the reason the genres row
            // has none. The heading carries the count, as a library band does,
            // because the count is the size of the shelf.
            Row::Franchises => {
                let items: Vec<Item> = source
                    .franchises()
                    .into_iter()
                    .map(franchise_item)
                    .collect();
                self.heading = format!("Franchises · {}", items.len());
                self.items = items;
            }
            // The libraries row reads the libraries. The banner row is here only
            // because the match is exhaustive: a strip never carries it, because
            // the banner is read off the strips and not from a row of its own.
            Row::Libraries | Row::Banner => {
                self.heading = "Libraries".to_string();
                self.items = source.libraries().into_iter().map(library_item).collect();
            }
        }
        screens::fitted_strip(&mut self.items);
        self.focus = self.focus.min(self.count().saturating_sub(1));
    }

    // The slots a press can reach: the items, and the "see all" slot
    // after them.
    pub(super) fn count(&self) -> usize {
        self.items.len() + usize::from(self.last.is_some())
    }

    pub(super) fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    // The item that holds focus, or nothing while "see all" holds it.
    pub(super) fn focused(&self) -> Option<&Item> {
        self.items.get(self.focus)
    }

    // What a select opens. "See all" opens the page the strip is about:
    // a person's own page for a person's strip, and the wall of everything
    // the query answers for every other. A library opens its wall, and a
    // title opens its page by its kind.
    pub(super) fn select(&self, source: &mut dyn Source) -> Step {
        let Some(item) = self.focused() else {
            return match &self.row {
                Row::Query(query) if self.last.is_some() => slots::see_all(query, source),
                _ => Step::Stay,
            };
        };
        if item.kind == LIBRARY {
            let query = Query::Library {
                library: item.library.clone(),
            };
            return Step::Open(Screen::Wall(Wall::open(query, source)));
        }
        if item.kind == GENRE {
            let query = Query::Genre {
                name: item.id.clone(),
                order: Order::Released,
            };
            return slots::see_all(&query, source);
        }
        if item.kind == FRANCHISE {
            return match franchise::Franchise::open(&item.library, &item.id, source) {
                Some(page) => Step::Open(Screen::Franchise(Box::new(page))),
                None => Step::Stay,
            };
        }
        slots::opened(item, source)
    }
}

// One library as a slot of the libraries strip: its name as the caption,
// how many of what it holds under it, and the posters of its newest-added
// titles as the mosaic.
fn library_item(entry: LibraryEntry) -> Item {
    let name = library_name(&entry.library).to_string();
    let under = facts::counted(entry.items as i64, &entry.kind);
    let tiles = entry
        .art
        .into_iter()
        .map(|art| (entry.library.clone(), art))
        .collect();
    Item {
        id: entry.library.clone(),
        library: entry.library,
        art_library: String::new(),
        kind: LIBRARY.to_string(),
        fitted: name.clone(),
        caption: name.clone(),
        line: facts::Line::of(&[&name]),
        name,
        under_fitted: under.clone(),
        under,
        tagline: false,
        art: String::new(),
        tiles,
        episode: None,
        new: 0,
    }
}

// One genre as a slot of the genres strip: the genre as the caption, the
// count of its titles under it, and the posters of its newest titles as
// the mosaic. No poster stands on two tiles of the row.
fn genre_item(entry: GenreEntry) -> Item {
    let titles = facts::counted(entry.titles as i64, "titles");
    Item {
        id: entry.name.clone(),
        library: String::new(),
        art_library: String::new(),
        kind: GENRE.to_string(),
        fitted: entry.name.clone(),
        caption: entry.name.clone(),
        line: facts::Line::of(&[&entry.name]),
        name: entry.name,
        under_fitted: titles.clone(),
        under: titles,
        tagline: false,
        art: String::new(),
        tiles: entry.art,
        episode: None,
        new: 0,
    }
}

// One franchise as a slot of the franchises strip: its title as the caption,
// and the art beside its franchise.yaml as the poster. The slot draws its
// title on the tile where the row carries no art, which is what every slot
// with no art draws. The library and the id are the ones a press opens the
// page by.
// The second line is the scope of the order, in the words a franchise
// strip's heading carries after the name.
fn franchise_item(entry: FranchiseEntry) -> Item {
    let under = franchise::strips::counted(entry.movies, entry.series);
    Item {
        id: entry.id,
        library: entry.library,
        art_library: entry.art_library,
        kind: FRANCHISE.to_string(),
        fitted: entry.title.clone(),
        caption: entry.title.clone(),
        line: facts::Line::of(&[&entry.title]),
        name: entry.title,
        under_fitted: under.clone(),
        under,
        tagline: false,
        art: entry.art,
        tiles: Vec::new(),
        episode: None,
        new: 0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::recency::{WORKS_FLOOR, date_seconds};
    use crate::catalog::{Slot, TILES};
    use crate::sample::Catalog;
    use crate::views::Card;

    fn work(parts: &str) -> Slot {
        Slot {
            parts: parts.into(),
            ..Slot::default()
        }
    }

    fn credited() -> Query {
        Query::Person {
            library: "sample/features".into(),
            path: ".contributors/A Player".into(),
        }
    }

    // One work of a person's read as its card draws it: a film of 1987,
    // 1h 37m long, rated PG-13, credited with these parts, under a
    // heading the whole read wrote. Both lines, because the parts left
    // decide which of the two the title stands on.
    fn carded(read: &[&str], parts: &str) -> (String, String) {
        let mut works: Vec<Slot> = read.iter().map(|parts| work(parts)).collect();
        works.push(Slot {
            kind: "movies".into(),
            title: "The Show".into(),
            released: "1987".into(),
            duration: 5_820,
            rating: "PG-13".into(),
            parts: parts.into(),
            ..Slot::default()
        });
        credits::credit(&credited(), &mut works);
        let item = Item::of(&credited(), works.pop().expect("the work under test"));
        (item.caption, item.under)
    }

    #[test]
    fn a_card_drops_the_role_a_one_role_heading_names_and_keeps_every_other_part() {
        let cases: [(&[&str], &str, (&str, &str)); 8] = [
            (
                &["Director", "Director"],
                "Director",
                ("The Show", "Film · 1987"),
            ),
            (
                &["as Samara Morgan"],
                "as Samara Morgan",
                ("Samara Morgan", "The Show · 1987"),
            ),
            (
                &["Director", "Writer"],
                "Director, Writer",
                ("The Show", "Director, Writer · 1987"),
            ),
            (
                &["Director", "Writer"],
                "Writer",
                ("The Show", "Writer · 1987"),
            ),
            (
                &["as The Lead", "Director"],
                "as The Lead",
                ("The Lead", "The Show · 1987"),
            ),
            (
                &["as The Lead", "Director"],
                "Director",
                ("The Show", "Director · 1987"),
            ),
            (
                &["as The Lead", "Director"],
                "Director, as The Lead",
                ("The Show", "Director, as The Lead · 1987"),
            ),
            (
                &["as One, as Two"],
                "as One, as Two",
                ("One, Two", "The Show · 1987"),
            ),
        ];
        for (read, parts, (caption, under)) in cases {
            let carded = carded(read, parts);
            assert_eq!(carded, (caption.to_string(), under.to_string()), "{parts}");
        }
    }

    // One slot of a title too long for a poster's caption band, with parts
    // too long for the line under it.
    fn wide(episode: Option<crate::catalog::InSeries>) -> Slot {
        Slot {
            title: "W".repeat(60),
            released: "1987".into(),
            parts: "as ".to_string() + &"W".repeat(60),
            episode,
            ..Slot::default()
        }
    }

    #[test]
    fn a_card_carries_both_its_lines_cut_to_the_band_its_ratio_draws() {
        let query = Query::Person {
            library: "sample/features".into(),
            path: ".contributors/A Player".into(),
        };
        let mut items = vec![Item::of(&query, wide(None))];
        screens::fitted_strip(&mut items);
        let band = strip::caption_width(crate::views::wall::POSTER);
        assert!(items[0].fitted.ends_with('\u{2026}'));
        assert!(items[0].under_fitted.ends_with('\u{2026}'));
        assert!(crate::views::text::measured(&items[0].fitted, crate::look::CAPTION) <= band);
        assert!(crate::views::text::measured(&items[0].under_fitted, crate::look::FACE) <= band);
    }

    #[test]
    fn a_still_card_cuts_its_lines_to_the_wider_band_a_still_draws_under() {
        let query = Query::Released { fold: Fold::Airing };
        let mut items = vec![Item::of(
            &query,
            wide(Some(crate::catalog::InSeries {
                series: "series:1".into(),
                name: "W".repeat(60),
                season: 3,
                episode: 4,
            })),
        )];
        screens::fitted_strip(&mut items);
        let still = strip::caption_width(crate::views::wall::STILL);
        let poster = strip::caption_width(crate::views::wall::POSTER);
        let drawn = crate::views::text::measured(&items[0].fitted, crate::look::CAPTION);
        assert!(drawn <= still);
        assert!(drawn > poster);
    }

    #[test]
    fn a_card_whose_lines_fit_the_band_is_cut_nowhere() {
        let mut items = vec![library_item(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 42,
            art: Vec::new(),
        })];
        screens::fitted_strip(&mut items);
        assert_eq!(items[0].fitted, "features");
        assert_eq!(items[0].under_fitted, "42 movies");
    }

    #[test]
    fn a_drawn_set_of_three_films_holds_nothing() {
        let mut strip = Strip::new(Row::Query(Query::Set {
            library: "sample/features".into(),
            id: "set:sample:01".into(),
        }));
        strip.reread(&mut Catalog, 0, &[]);
        assert!(strip.is_empty());
        assert_eq!(strip.last, None);
        assert_eq!(strip.count(), 0);
    }

    #[test]
    fn a_drawn_person_of_three_works_holds_nothing_and_loses_the_slot_about_them() {
        let mut strip = Strip::new(Row::Query(Query::Person {
            library: "sample/features".into(),
            path: ".contributors/Player 0001-1".into(),
        }));
        strip.reread(&mut Catalog, 0, &[]);
        assert!(strip.is_empty());
        assert_eq!(strip.last, None);
    }

    #[test]
    fn a_drawn_strip_over_the_floor_reads_every_slot_it_answered() {
        let mut strip = Strip::new(Row::Query(Query::Person {
            library: "sample/features".into(),
            path: ".contributors/A Second Writer".into(),
        }));
        strip.reread(&mut Catalog, 0, &[]);
        assert_eq!(strip.items.len(), 6);
        assert!(strip.items.len() >= FLOOR);
        assert!(strip.last.is_some());
    }

    #[test]
    fn a_person_the_pool_admits_always_meets_the_draw_floor() {
        const { assert!(FLOOR as u64 == WORKS_FLOOR + 1) };
    }

    #[test]
    fn a_recency_strip_under_the_floor_still_holds_its_slots() {
        let today = date_seconds("2026-09-05").expect("a full date reads");
        let mut strip = Strip::new(Row::Query(Query::Released { fold: Fold::Airing }));
        strip.reread(&mut Catalog, today, &[]);
        assert!(!strip.is_empty());
        assert!(strip.items.len() < FLOOR);
    }

    #[test]
    fn the_libraries_row_of_two_libraries_still_holds_them() {
        let mut strip = Strip::new(Row::Libraries);
        strip.reread(&mut Catalog, 0, &[]);
        assert_eq!(strip.items.len(), 2);
        assert!(strip.items.len() < FLOOR);
    }

    #[test]
    fn the_genres_and_the_franchises_rows_hold_what_they_read() {
        let mut genres = Strip::new(Row::Genres);
        genres.reread(&mut Catalog, 0, &[]);
        assert_eq!(genres.items.len(), 5);
        let mut franchises = Strip::new(Row::Franchises);
        franchises.reread(&mut Catalog, 0, &[]);
        assert!(!franchises.is_empty());
    }

    #[test]
    fn a_persons_strip_is_headed_by_their_name_and_their_roles() {
        let mut strip = Strip::new(Row::Query(Query::Person {
            library: "sample/features".into(),
            path: ".contributors/A Second Writer".into(),
        }));
        strip.reread(&mut Catalog, 0, &[]);
        assert_eq!(strip.heading, "A Second Writer · writer");
        let (name, rest) = crate::views::strip::split(&strip.heading);
        assert_eq!((name, rest), ("A Second Writer", " · writer"));
    }

    // The strip of one invented person, read from the sample catalog.
    fn person(path: &str) -> Strip {
        let mut strip = Strip::new(Row::Query(Query::Person {
            library: "sample/features".into(),
            path: path.into(),
        }));
        strip.reread(&mut Catalog, 0, &[]);
        strip
    }

    #[test]
    fn a_card_under_a_one_role_heading_drops_that_role_from_its_second_line() {
        let strip = person(".contributors/A Second Writer");
        assert_eq!(strip.heading, "A Second Writer · writer");
        assert_eq!(strip.items[0].caption, strip.items[0].name);
        assert_eq!(strip.items[0].under, "Film · 2011");
    }

    #[test]
    fn a_card_under_an_actors_heading_leads_with_the_character() {
        let query = Query::Person {
            library: "sample/features".into(),
            path: ".contributors/Player 0001-1".into(),
        };
        let mut works =
            crate::sample::people::works("sample/features", ".contributors/Player 0001-1");
        assert_eq!(credits::credit(&query, &mut works), "actor");
        let item = Item::of(&query, works.remove(0));
        assert_eq!(item.caption, "Part 1");
        assert_eq!(item.under, "Specimen 0003 · 2011");
    }

    #[test]
    fn the_page_reads_the_banner_the_recency_rows_the_draw_the_libraries_the_genres_then_the_franchises()
     {
        let western = Query::Genre {
            name: "Western".into(),
            order: crate::catalog::Order::Released,
        };
        let drawn = vec![Candidate {
            query: western.clone(),
            name: "Western".into(),
            weight: 7,
        }];
        let fold = Fold::Shows { today: 0 };
        assert_eq!(
            rows(0, drawn),
            [
                Row::Banner,
                Row::Query(Query::Released { fold }),
                Row::Query(Query::Added { fold }),
                Row::Query(western),
                Row::Libraries,
                Row::Genres,
                Row::Franchises,
            ]
        );
        assert_eq!(rows(0, Vec::new()).len(), 6);
    }

    #[test]
    fn only_the_two_recency_rows_are_recency() {
        assert!(Row::Query(Query::Released { fold: Fold::Airing }).recency());
        assert!(Row::Query(Query::Added { fold: Fold::Titles }).recency());
        assert!(!Row::Banner.recency());
        assert!(!Row::Libraries.recency());
        assert!(!Row::Genres.recency());
        assert!(!Row::Franchises.recency());
        assert!(
            !Row::Query(Query::Library {
                library: "sample/features".into()
            })
            .recency()
        );
    }

    #[test]
    fn a_library_is_a_slot_with_its_name_its_count_and_a_mosaic_of_its_newest_posters() {
        let item = library_item(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 1_422,
            art: vec!["posters/one.jpg".into(), "posters/two.jpg".into()],
        });
        assert_eq!(item.kind, LIBRARY);
        assert_eq!(item.id, "screening/features");
        assert_eq!(item.library, "screening/features");
        assert_eq!(item.name, "features");
        assert_eq!(item.caption, "features");
        assert_eq!(item.fitted, "features");
        assert_eq!(item.line.words(), "features");
        assert_eq!(item.under, "1,422 movies");
        assert_eq!(item.art, "");
        assert_eq!(
            item.tiles,
            [
                (
                    "screening/features".to_string(),
                    "posters/one.jpg".to_string()
                ),
                (
                    "screening/features".to_string(),
                    "posters/two.jpg".to_string()
                ),
            ]
        );
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_library_of_one_counts_it_in_the_singular_and_a_library_of_series_never_does() {
        let under = |items, kind: &str| {
            library_item(LibraryEntry {
                library: "screening/features".into(),
                kind: kind.into(),
                items,
                art: Vec::new(),
            })
            .under
        };
        assert_eq!(under(1, "movies"), "1 movie");
        assert_eq!(under(1, "series"), "1 series");
        assert_eq!(under(2, "movies"), "2 movies");
        assert_eq!(under(165, "series"), "165 series");
    }

    #[test]
    fn a_genre_is_a_slot_with_its_name_its_count_and_a_mosaic_of_its_own_posters() {
        let item = genre_item(GenreEntry {
            name: "Western".into(),
            titles: 42,
            art: vec![
                ("screening/features".into(), "posters/one.jpg".into()),
                ("screening/serials".into(), "posters/two.jpg".into()),
            ],
        });
        assert_eq!(item.kind, GENRE);
        assert_eq!(item.id, "Western");
        assert_eq!(item.library, "");
        assert_eq!(item.name, "Western");
        assert_eq!(item.caption, "Western");
        assert_eq!(item.fitted, "Western");
        assert_eq!(item.line.words(), "Western");
        assert_eq!(item.under, "42 titles");
        assert_eq!(item.art, "");
        assert_eq!(
            item.tiles,
            [
                (
                    "screening/features".to_string(),
                    "posters/one.jpg".to_string()
                ),
                (
                    "screening/serials".to_string(),
                    "posters/two.jpg".to_string()
                ),
            ]
        );
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_franchise_is_a_slot_with_its_title_and_the_art_beside_its_file() {
        let item = franchise_item(FranchiseEntry {
            library: "screening/orders".into(),
            id: "franchise:name:the-cycle".into(),
            title: "The Cycle".into(),
            art: "the-cycle/poster.jpg".into(),
            art_library: "screening/films".into(),
            slug: "the-cycle".into(),
            movies: 26,
            series: 14,
        });
        assert_eq!(item.kind, FRANCHISE);
        assert_eq!(item.id, "franchise:name:the-cycle");
        assert_eq!(item.library, "screening/orders");
        // The art of a franchise is a member's poster on the member's
        // own volume, so the slot resolves it there.
        assert_eq!(item.art_library, "screening/films");
        assert_eq!(Card::library(&item), "screening/films");
        assert_eq!(item.name, "The Cycle");
        assert_eq!(item.caption, "The Cycle");
        assert_eq!(item.fitted, "The Cycle");
        assert_eq!(item.line.words(), "The Cycle");
        assert_eq!(item.under, "40 films and series");
        assert_eq!(item.art, "the-cycle/poster.jpg");
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_franchise_with_no_art_draws_the_tile_of_its_title() {
        let item = franchise_item(FranchiseEntry {
            title: "The Saga".into(),
            movies: 1,
            ..FranchiseEntry::default()
        });
        assert_eq!(item.art, "");
        assert_eq!(item.name, "The Saga");
    }

    #[test]
    fn every_tile_of_the_franchises_row_says_the_scope_of_its_order() {
        let mut strip = Strip::new(Row::Franchises);
        strip.reread(&mut Catalog, 0, &[]);
        let scopes: Vec<&str> = strip
            .items
            .iter()
            .map(|item| item.under_fitted.as_str())
            .collect();
        assert_eq!(scopes, ["9 films and series", "3 films"]);
    }

    #[test]
    fn every_still_of_a_recency_row_reads_its_episode_over_its_show() {
        let today = date_seconds("2026-09-05").expect("a full date reads");
        let mut strip = Strip::new(Row::Query(Query::Released { fold: Fold::Airing }));
        strip.reread(&mut Catalog, today, &[]);
        let stills: Vec<&Item> = strip
            .items
            .iter()
            .filter(|item| item.episode.is_some())
            .collect();
        assert!(!stills.is_empty());
        for still in stills {
            assert!(still.caption.starts_with("Segment "), "{}", still.caption);
            let (show, rest) = still
                .under
                .split_once(" · ")
                .expect("a still carries its show and its numbers under it");
            assert!(show.starts_with("Serial "), "{}", still.under);
            assert!(rest.starts_with('S'), "{}", still.under);
            assert!(still.under.ends_with('m'), "{}", still.under);
        }
    }

    #[test]
    fn a_series_of_a_genre_row_carries_its_season_count_under_it() {
        let mut strip = Strip::new(Row::Query(Query::Genre {
            name: "Drama".into(),
            order: Order::Released,
        }));
        strip.reread(&mut Catalog, 0, &[]);
        let serial = strip
            .items
            .iter()
            .find(|item| item.kind == "series")
            .expect("the sample's serials all lead with Drama");
        assert_eq!(serial.under, "2025 · 2 seasons · TV-14");
    }

    #[test]
    fn a_genre_one_title_carries_counts_it_in_the_singular() {
        let item = genre_item(GenreEntry {
            name: "Silent".into(),
            titles: 1,
            art: Vec::new(),
        });
        assert_eq!(item.under, "1 title");
        assert_eq!(item.art, "");
        assert!(item.tiles.is_empty());
    }

    #[test]
    fn every_shelf_of_the_sample_draws_a_mosaic_and_no_title_does() {
        let mut strip = Strip::new(Row::Libraries);
        strip.reread(&mut Catalog, 0, &[]);
        assert!(strip.items.iter().all(|item| item.tiles.len() == TILES));
        let mut genres = Strip::new(Row::Genres);
        genres.reread(&mut Catalog, 0, &[]);
        assert!(genres.items.iter().all(|item| item.tiles.len() == TILES));
        let mut franchises = Strip::new(Row::Franchises);
        franchises.reread(&mut Catalog, 0, &[]);
        assert!(franchises.items.iter().all(|item| item.tiles.is_empty()));
    }

    #[test]
    fn no_poster_of_the_genres_row_stands_on_two_tiles() {
        let mut strip = Strip::new(Row::Genres);
        strip.reread(&mut Catalog, 0, &[]);
        let mut drawn: Vec<&(String, String)> =
            strip.items.iter().flat_map(|item| &item.tiles).collect();
        let posters = drawn.len();
        drawn.sort();
        drawn.dedup();
        assert_eq!(drawn.len(), posters);
    }
}
