// The rows of the home page and the strip that reads one. A row names
// what a strip reads: the slots of one query, the libraries, or the
// genres. The strip holds the read's items, the focus, and what a select
// opens. The page's order is fixed here, and the home page and its read
// both take it from here.

use crate::catalog::pool::Candidate;
use crate::catalog::recency::SHOWN;
use crate::catalog::{
    Fold, FranchiseEntry, GenreEntry, LibraryEntry, Order, Query, Slot, Source, library_name,
};
use crate::screens::wall::Wall;
use crate::screens::{Item, Screen, Step, facts, franchise, person, slots};
use crate::views::strip;

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
// strips under the `Airing` fold because the home page shows what is
// new, the strips the day drew in the drawn order, the libraries, and
// the genres, and the franchises to close the page.
pub(super) fn rows(drawn: Vec<Candidate>) -> Vec<Row> {
    let mut rows = vec![
        Row::Banner,
        Row::Query(Query::Released { fold: Fold::Airing }),
        Row::Query(Query::Added { fold: Fold::Airing }),
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
            lines: 2,
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
    pub(super) fn reread(&mut self, source: &mut dyn Source, today: i64, released: &[Item]) {
        match &self.row {
            Row::Query(query) => {
                let answer = source.wall(query);
                // A person's heading is two-tone: the name bright, and
                // after the dot the person's roles across the strip's
                // works, most frequent first.
                self.heading = match query {
                    Query::Person { .. } => facts::joined(&[&answer.name, &roles(&answer.slots)]),
                    _ => query.name(&answer.name),
                };
                let answered = answer.slots.len();
                let slots = match query {
                    Query::Released { .. } => super::recent::released(answer.slots, today),
                    Query::Added { .. } => super::recent::added(answer.slots, released),
                    _ => answer.slots.into_iter().take(SHOWN).collect(),
                };
                self.last = match query {
                    Query::Person { library, path } => {
                        Some(Last::about(library, path, &answer.name, source))
                    }
                    _ => (slots.len() < answered).then(|| Last::words(strip::SEE_ALL.to_string())),
                };
                self.items = slots
                    .into_iter()
                    .map(|slot| Item::of(query, slot))
                    .collect();
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

// One library as a slot of the libraries strip: its name as the
// caption, its kind and count under it, and the poster of its
// newest-added title as the art.
fn library_item(entry: LibraryEntry) -> Item {
    let name = library_name(&entry.library).to_string();
    Item {
        id: entry.library.clone(),
        library: entry.library,
        art_library: String::new(),
        kind: LIBRARY.to_string(),
        caption: name.clone(),
        line: facts::Line::of(&[&name]),
        name,
        under: format!("{} · {}", entry.kind, entry.items),
        art: entry.art,
        episode: None,
    }
}

// One genre as a slot of the genres strip: the genre as the caption,
// the count of its titles under it, and the poster of its newest-released
// title as the art. Two genres may share one poster.
fn genre_item(entry: GenreEntry) -> Item {
    let titles = match entry.titles {
        1 => "1 title".to_string(),
        titles => format!("{titles} titles"),
    };
    Item {
        id: entry.name.clone(),
        library: entry.library,
        art_library: String::new(),
        kind: GENRE.to_string(),
        caption: entry.name.clone(),
        line: facts::Line::of(&[&entry.name]),
        name: entry.name,
        under: titles,
        art: entry.art,
        episode: None,
    }
}

// One franchise as a slot of the franchises strip: its title as the caption,
// and the art beside its franchise.yaml as the poster. The slot draws its
// title on the tile where the row carries no art, which is what every slot
// with no art draws. The library and the id are the ones a press opens the
// page by.
fn franchise_item(entry: FranchiseEntry) -> Item {
    Item {
        id: entry.id,
        library: entry.library,
        art_library: entry.art_library,
        kind: FRANCHISE.to_string(),
        caption: entry.title.clone(),
        line: facts::Line::of(&[&entry.title]),
        name: entry.title,
        under: String::new(),
        art: entry.art,
        episode: None,
    }
}

/// The person's roles across these works as role words in lower case,
/// comma separated, most frequent first. A work's parts line is what the
/// works read wrote: the role words and `as <character>` runs, comma
/// separated, and an `as` run is the actor role, never the character's
/// name. A role counts once per work even where a work credits it twice.
/// A tie in frequency keeps the order the roles first came in, so the
/// heading is stable between frames.
pub fn roles(slots: &[Slot]) -> String {
    let mut counted: Vec<(String, usize)> = Vec::new();
    for slot in slots {
        let mut seen: Vec<String> = Vec::new();
        for part in slot.parts.split(", ").filter(|part| !part.is_empty()) {
            let role = match part.starts_with("as ") {
                true => "actor".to_string(),
                false => part.to_lowercase(),
            };
            if seen.contains(&role) {
                continue;
            }
            seen.push(role.clone());
            match counted.iter_mut().find(|(named, _)| *named == role) {
                Some((_, count)) => *count += 1,
                None => counted.push((role, 1)),
            }
        }
    }
    counted.sort_by_key(|(_, count)| std::cmp::Reverse(*count));
    counted
        .into_iter()
        .map(|(role, _)| role)
        .collect::<Vec<String>>()
        .join(", ")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;
    use crate::views::Card;

    fn work(parts: &str) -> Slot {
        Slot {
            parts: parts.into(),
            ..Slot::default()
        }
    }

    #[test]
    fn a_persons_roles_read_most_frequent_first_in_lower_case() {
        let works = [
            work("as Ripley"),
            work("Writer"),
            work("as Dallas"),
            work("as Kane"),
            work("as Ash"),
            work("as Parker"),
        ];
        assert_eq!(roles(&works), "actor, writer");
    }

    #[test]
    fn roles_of_one_frequency_keep_the_order_they_first_came_in() {
        let works = [work("Writer"), work("Director"), work("as Someone")];
        assert_eq!(roles(&works), "writer, director, actor");
        let works = [work("Director"), work("Writer")];
        assert_eq!(roles(&works), "director, writer");
    }

    #[test]
    fn a_work_that_credits_a_person_twice_counts_once_per_role() {
        let works = [
            work("as One, as Two"),
            work("Writer, Director"),
            work("Writer"),
        ];
        assert_eq!(roles(&works), "writer, actor, director");
    }

    #[test]
    fn one_role_reads_as_one_word_and_no_work_reads_as_nothing() {
        assert_eq!(roles(&[work("Writer"), work("Writer")]), "writer");
        assert_eq!(roles(&[work("as The Part")]), "actor");
        assert_eq!(roles(&[]), "");
        assert_eq!(roles(&[work("")]), "");
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
        assert_eq!(
            rows(drawn),
            [
                Row::Banner,
                Row::Query(Query::Released { fold: Fold::Airing }),
                Row::Query(Query::Added { fold: Fold::Airing }),
                Row::Query(western),
                Row::Libraries,
                Row::Genres,
                Row::Franchises,
            ]
        );
        assert_eq!(rows(Vec::new()).len(), 6);
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
    fn a_library_is_a_slot_with_its_name_its_count_and_its_newest_poster() {
        let item = library_item(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 42,
            art: "posters/newest.jpg".into(),
        });
        assert_eq!(item.kind, LIBRARY);
        assert_eq!(item.id, "screening/features");
        assert_eq!(item.library, "screening/features");
        assert_eq!(item.name, "features");
        assert_eq!(item.caption, "features");
        assert_eq!(item.line.words(), "features");
        assert_eq!(item.under, "movies · 42");
        assert_eq!(item.art, "posters/newest.jpg");
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_genre_is_a_slot_with_its_name_its_count_and_its_newest_poster() {
        let item = genre_item(GenreEntry {
            name: "Western".into(),
            titles: 42,
            library: "screening/features".into(),
            art: "posters/newest.jpg".into(),
        });
        assert_eq!(item.kind, GENRE);
        assert_eq!(item.id, "Western");
        assert_eq!(item.library, "screening/features");
        assert_eq!(item.name, "Western");
        assert_eq!(item.caption, "Western");
        assert_eq!(item.line.words(), "Western");
        assert_eq!(item.under, "42 titles");
        assert_eq!(item.art, "posters/newest.jpg");
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
        assert_eq!(item.line.words(), "The Cycle");
        assert_eq!(item.under, "");
        assert_eq!(item.art, "the-cycle/poster.jpg");
        assert_eq!(item.episode, None);
    }

    #[test]
    fn a_franchise_with_no_art_draws_the_tile_of_its_title() {
        let item = franchise_item(FranchiseEntry {
            title: "The Saga".into(),
            ..FranchiseEntry::default()
        });
        assert_eq!(item.art, "");
        assert_eq!(item.name, "The Saga");
    }

    #[test]
    fn a_genre_one_title_carries_counts_it_in_the_singular() {
        let item = genre_item(GenreEntry {
            name: "Silent".into(),
            titles: 1,
            library: String::new(),
            art: String::new(),
        });
        assert_eq!(item.under, "1 title");
        assert_eq!(item.art, "");
    }
}
