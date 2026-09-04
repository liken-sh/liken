// The rows of the home page and the strip that reads one. A row names
// what a strip reads: the slots of one query, the libraries, or the
// genres. The strip holds the read's items, the focus, and what a select
// opens. The page's order is fixed here, and the home page and its read
// both take it from here.

use crate::catalog::pool::Candidate;
use crate::catalog::recency::SHOWN;
use crate::catalog::{Fold, GenreEntry, LibraryEntry, Order, Query, Source, library_name};
use crate::screens::wall::Wall;
use crate::screens::{Item, Screen, Step, facts, slots};
use crate::views::strip;

// The kind an item of the libraries strip carries, so a select on it
// opens the library's wall and never a page.
pub(super) const LIBRARY: &str = "library";

// The kind an item of the genres strip carries, so a select on it opens
// the genre's page and never a title's.
pub(super) const GENRE: &str = "genre";

/// One row of the page as a read: the banner, the slots of one query,
/// the libraries themselves, or the genres themselves.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Row {
    Banner,
    Query(Query),
    Libraries,
    Genres,
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
// the genres to close the page.
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
    rows
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
    pub last: Option<String>,
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
                self.heading = query.name(&answer.name);
                let answered = answer.slots.len();
                let slots = match query {
                    Query::Released { .. } => super::recent::released(answer.slots, today),
                    Query::Added { .. } => super::recent::added(answer.slots, released),
                    _ => answer.slots.into_iter().take(SHOWN).collect(),
                };
                self.last = match query {
                    Query::Person { .. } => Some(format!("About {}", answer.name)),
                    _ => (slots.len() < answered).then(|| strip::SEE_ALL.to_string()),
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
        kind: GENRE.to_string(),
        caption: entry.name.clone(),
        line: facts::Line::of(&[&entry.name]),
        name: entry.name,
        under: titles,
        art: entry.art,
        episode: None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_page_reads_the_banner_the_recency_rows_the_draw_the_libraries_then_the_genres() {
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
            ]
        );
        assert_eq!(rows(Vec::new()).len(), 5);
    }

    #[test]
    fn only_the_two_recency_rows_are_recency() {
        assert!(Row::Query(Query::Released { fold: Fold::Airing }).recency());
        assert!(Row::Query(Query::Added { fold: Fold::Titles }).recency());
        assert!(!Row::Banner.recency());
        assert!(!Row::Libraries.recency());
        assert!(!Row::Genres.recency());
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
