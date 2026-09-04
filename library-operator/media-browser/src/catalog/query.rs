// Every screen of titles is one Query and one wall. A Query is a value
// of a closed set of shapes. Each shape has a heading a person reads, an
// order, and a read the catalog answers from an index, and none is a
// string of SQL: a string cannot be named in a heading and cannot promise
// an indexed read. This module holds the Query, the Slot a read answers
// with, and the Answer that names what the query is about.

use super::{Title, library_name};

/// How a recency query treats episodes. An episode is the only new
/// thing about a series, and without a fold a season drop is ten slots
/// that hide everything else. `Titles` folds every episode to its series.
/// `Episodes` folds none. `Airing` keeps an episode released within the
/// window of its arrival and folds the rest.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Fold {
    Titles,
    Episodes,
    Airing,
}

/// The queries a wall can be fed. The set is closed and grows by one
/// variant per plan. `Library` is one library in sort order. `Person` is
/// every work of one person across the libraries. `Set` is the members of
/// one set in release order. `Released` is movies and episodes newest
/// release first, and `Added` is the same newest arrival first, both
/// across every library and both folded by their `Fold`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Query {
    Library { library: String },
    Person { library: String, path: String },
    Set { library: String, id: String },
    Released { fold: Fold },
    Added { fold: Fold },
}

impl Query {
    /// The heading without the count, which is what a strip draws over its
    /// slots. A person's and a set's name comes with the answer, because only
    /// the catalog holds it.
    pub fn name(&self, name: &str) -> String {
        match self {
            Self::Library { library } => library_name(library).to_string(),
            Self::Person { .. } | Self::Set { .. } => name.to_string(),
            Self::Released { .. } => "Recently released".to_string(),
            Self::Added { .. } => "Recently added".to_string(),
        }
    }

    /// The heading the band draws over this query's slots. A library's
    /// heading carries its count. A person's and a set's carry the name
    /// alone, and that name comes with the answer, because only the catalog
    /// holds it.
    /// The heading the band draws over this query's slots. A library's
    /// heading and a recency query's carry the count. A person's and a set's
    /// carry the name alone.
    pub fn heading(&self, name: &str, count: usize) -> String {
        match self {
            Self::Person { .. } | Self::Set { .. } => name.to_string(),
            _ => format!("{} · {count}", self.name(name)),
        }
    }

    /// The query a "see all" slot opens. A recency query opens itself with
    /// every episode folded to its series, so the wall stays all posters at
    /// one ratio and no wall ever holds a still. Every other query opens
    /// itself.
    pub fn all_titles(&self) -> Self {
        match self {
            Self::Released { .. } => Self::Released { fold: Fold::Titles },
            Self::Added { .. } => Self::Added { fold: Fold::Titles },
            _ => self.clone(),
        }
    }
}

/// What an episode slot carries beyond a title: the series it is in,
/// that series' name for the caption, and the aired numbers a select
/// opens the series page on. A slot that holds one draws as a still at
/// 16:9, and every other slot draws as a poster.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct InSeries {
    pub series: String,
    pub name: String,
    pub season: i64,
    pub episode: i64,
}

/// One title as a read answers it. Every slot carries its own library
/// and kind, because no wall fixes up front what a select opens, and a
/// person's works span libraries. `parts` is empty on every read but a
/// person's.
/// One title as a read answers it. Every slot carries its own library
/// and kind, because no wall fixes up front what a select opens, and a
/// person's works span libraries. `parts` is empty on every read but a
/// person's. `episode` is set on an episode slot alone, which the recency
/// queries answer with under the `Episodes` and `Airing` folds.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Slot {
    pub library: String,
    pub kind: String,
    pub id: String,
    pub title: String,
    pub released: String,
    pub art: String,
    pub duration: i64,
    pub rating: String,
    pub parts: String,
    pub episode: Option<InSeries>,
}

impl Slot {
    /// One title row as a slot of the library and kind that hold it, with
    /// no parts.
    pub fn of(library: &str, kind: &str, title: Title) -> Self {
        Self {
            library: library.to_string(),
            kind: kind.to_string(),
            id: title.id,
            title: title.title,
            released: title.released,
            art: title.art,
            duration: title.duration,
            rating: title.rating,
            parts: String::new(),
            episode: None,
        }
    }

    /// Whether the slot's art is a still at 16:9 and not a poster at 2:3.
    /// Only an episode's is.
    pub fn still(&self) -> bool {
        self.episode.is_some()
    }
}

/// What a source answers a query with. The name is what the query is
/// about, and a person's or a set's heading is that name. An empty name
/// means the query named nothing the catalog holds.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Answer {
    pub name: String,
    pub slots: Vec<Slot>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_library_heading_carries_the_name_half_and_the_count() {
        let query = Query::Library {
            library: "screening/features".into(),
        };
        assert_eq!(query.heading("features", 42), "features · 42");
    }

    #[test]
    fn a_person_and_a_set_are_headed_by_their_name_alone() {
        let person = Query::Person {
            library: "screening/features".into(),
            path: ".contributors/A Player".into(),
        };
        assert_eq!(person.heading("A Player", 3), "A Player");
        let set = Query::Set {
            library: "screening/features".into(),
            id: "set:1".into(),
        };
        assert_eq!(set.heading("The Cycle", 3), "The Cycle");
    }

    #[test]
    fn the_recency_queries_are_headed_by_their_words_and_the_count() {
        let released = Query::Released { fold: Fold::Airing };
        assert_eq!(released.name(""), "Recently released");
        assert_eq!(released.heading("", 12), "Recently released · 12");
        let added = Query::Added { fold: Fold::Titles };
        assert_eq!(added.name("ignored"), "Recently added");
        assert_eq!(added.heading("ignored", 3), "Recently added · 3");
    }

    #[test]
    fn a_library_names_itself_without_the_count() {
        let query = Query::Library {
            library: "screening/features".into(),
        };
        assert_eq!(query.name(""), "features");
    }

    #[test]
    fn see_all_opens_a_recency_query_with_every_episode_folded() {
        assert_eq!(
            Query::Released { fold: Fold::Airing }.all_titles(),
            Query::Released { fold: Fold::Titles }
        );
        assert_eq!(
            Query::Added {
                fold: Fold::Episodes
            }
            .all_titles(),
            Query::Added { fold: Fold::Titles }
        );
        let library = Query::Library {
            library: "screening/features".into(),
        };
        assert_eq!(library.all_titles(), library);
    }

    #[test]
    fn an_episode_slot_is_a_still_and_every_other_slot_a_poster() {
        let episode = Slot {
            episode: Some(InSeries {
                series: "series:1".into(),
                name: "The Serial".into(),
                season: 3,
                episode: 4,
            }),
            ..Slot::default()
        };
        assert!(episode.still());
        assert!(!Slot::default().still());
    }

    #[test]
    fn a_slot_of_a_title_carries_its_library_and_kind_and_no_parts() {
        let slot = Slot::of(
            "screening/features",
            "movies",
            Title {
                id: "movie:1".into(),
                title: "Specimen 0001".into(),
                released: "1987".into(),
                art: "1.jpg".into(),
                duration: 5_820,
                rating: "PG-13".into(),
            },
        );
        assert_eq!(slot.library, "screening/features");
        assert_eq!(slot.kind, "movies");
        assert_eq!(slot.id, "movie:1");
        assert_eq!(slot.title, "Specimen 0001");
        assert_eq!(slot.duration, 5_820);
        assert_eq!(slot.rating, "PG-13");
        assert_eq!(slot.parts, "");
        assert!(!slot.still());
    }
}
