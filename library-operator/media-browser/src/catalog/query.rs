// Every screen of titles is one Query and one wall. A Query is a value
// of a closed set of shapes. Each shape has a heading a person reads, an
// order, and a read the source answers fast: from an index in SQL, or
// for `Search`, from the in-memory index. None is a string of SQL: a
// string cannot be named in a heading and cannot promise a fast read.
// This module holds the Query, the Slot a read answers with, and the
// Answer that names what the query is about.

use super::progress::Played;
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
    /// Every episode of a series folds to one slot, drawn as the still of
    /// the newest of them, which counts how many of them are current on
    /// `today`, in seconds.
    Shows {
        today: i64,
    },
}

/// The column a read orders by: the release date or the arrival. It is
/// a closed pair and not a column name, because the sidecar formats it
/// into SQL, and the genre read and the recency read share it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Order {
    Released,
    Added,
}

/// The three orders a library wall can be read in, which the rail's
/// button cycles. `Newest` and `Oldest` order by the release date and
/// `Title` by the sort key, "The Matrix" under M. Title is a library
/// wall's default, because a whole library is what a person walks by
/// name.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Sort {
    Newest,
    Oldest,
    #[default]
    Title,
}

impl Sort {
    /// The word the rail's sort button shows.
    pub fn word(&self) -> &'static str {
        match self {
            Self::Newest => "Newest",
            Self::Oldest => "Oldest",
            Self::Title => "Title",
        }
    }

    /// The next order in the cycle. The three orders are one ring, so a
    /// fourth press is back at the first.
    pub fn next(&self) -> Self {
        match self {
            Self::Title => Self::Newest,
            Self::Newest => Self::Oldest,
            Self::Oldest => Self::Title,
        }
    }
}

/// The four orders a genre wall can be read in. `Leads` is the titles
/// that lead with the genre first, then the rest, each run newest
/// first; it is a genre wall's default and its own type, so a library
/// wall cannot be asked for it. The other three are `Sort`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum GenreSort {
    #[default]
    Leads,
    By(Sort),
}

impl GenreSort {
    /// The word the rail's sort button shows.
    pub fn word(&self) -> &'static str {
        match self {
            Self::Leads => "Genre",
            Self::By(sort) => sort.word(),
        }
    }

    /// The next order in the cycle. The four orders are one ring back to
    /// `Leads`.
    pub fn next(&self) -> Self {
        match self {
            Self::Leads => Self::By(Sort::Newest),
            Self::By(Sort::Newest) => Self::By(Sort::Oldest),
            Self::By(Sort::Oldest) => Self::By(Sort::Title),
            Self::By(Sort::Title) => Self::Leads,
        }
    }
}

/// The queries a wall can be fed. The set is closed and grows by one
/// variant per plan. `Library` is one library in sort order. `Person` is
/// every work of one person across the libraries. `Set` is the members of
/// one set in release order. `Released` is movies and episodes newest
/// release first, and `Added` is the same newest arrival first, both
/// across every library and both folded by their `Fold`. `Genre` is every
/// movie and series across every library that carries the genre, in its
/// `GenreSort`; in `Leads`, the titles that lead with it come first and
/// the rest follow, each run newest by the order's column.
/// `Franchise` is the members of one franchise that some library holds,
/// in story order; it names the `Library` of kind franchises that holds
/// the order, and never a member's own library.
/// `Search` is the text a person typed, answered from the in-memory
/// search index and never from SQL, best hit first.
/// `Library` and `Genre` carry the order their rail's button cycles.
/// The recency queries are newest first by definition and carry none.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Query {
    Library {
        library: String,
        sort: Sort,
    },
    Person {
        library: String,
        path: String,
    },
    Set {
        library: String,
        id: String,
    },
    Released {
        fold: Fold,
    },
    Added {
        fold: Fold,
    },
    Genre {
        name: String,
        order: Order,
        sort: GenreSort,
    },
    Franchise {
        library: String,
        id: String,
    },
    Search {
        text: String,
    },
}

impl Query {
    /// The heading without the count, which is what a strip draws over its
    /// slots. A person's, a set's, and a franchise's name comes with the
    /// answer, because only the catalog holds it.
    /// A search is named by the text a person typed, which the query
    /// holds, so no answer carries a name for it.
    pub fn name(&self, name: &str) -> String {
        match self {
            Self::Library { library, .. } => library_name(library).to_string(),
            Self::Person { .. } | Self::Set { .. } | Self::Franchise { .. } => name.to_string(),
            Self::Released { .. } => "Recently released".to_string(),
            Self::Added { .. } => "Recently added".to_string(),
            Self::Genre { name, .. } => name.clone(),
            Self::Search { text } => text.clone(),
        }
    }

    /// The heading the band draws over this query's slots. A library's
    /// heading and a recency query's carry the count. A person's, a set's,
    /// and a franchise's carry the name alone.
    /// A genre's heading carries the counts by kind, "Science Fiction ·
    /// 429 movies, 70 series", which is what the head over the wall
    /// carried before the band took it.
    pub fn heading(&self, name: &str, counts: Counts) -> String {
        match self {
            Self::Person { .. } | Self::Set { .. } | Self::Franchise { .. } => name.to_string(),
            Self::Genre { .. } => format!("{} · {}", self.name(name), counts.words()),
            _ => format!("{} · {}", self.name(name), counts.items),
        }
    }

    /// The same query in the next order its wall cycles to. A query with
    /// no button is unchanged.
    pub fn resorted(&self) -> Self {
        match self.clone() {
            Self::Library { library, sort } => Self::Library {
                library,
                sort: sort.next(),
            },
            Self::Genre { name, order, sort } => Self::Genre {
                name,
                order,
                sort: sort.next(),
            },
            query => query,
        }
    }

    /// The word the sort button shows, or nothing on a wall that draws no
    /// button: the recency walls, a person, a set, a franchise, and a
    /// search.
    pub fn sort_word(&self) -> Option<&'static str> {
        match self {
            Self::Library { sort, .. } => Some(sort.word()),
            Self::Genre { sort, .. } => Some(sort.word()),
            _ => None,
        }
    }

    /// The query a "see all" slot opens. A recency query opens itself with
    /// every episode folded to its series, so the wall stays all posters at
    /// one ratio and no wall ever holds a still. Every other query opens
    /// itself.
    pub fn all_titles(&self) -> Self {
        match self.clone() {
            Self::Released { .. } => Self::Released { fold: Fold::Titles },
            Self::Added { .. } => Self::Added { fold: Fold::Titles },
            query => query,
        }
    }
}

/// What a heading counts: every item, and the split by kind a genre's
/// heading reads.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Counts {
    pub items: usize,
    pub movies: usize,
    pub series: usize,
}

impl Counts {
    /// The counts over the kind word of every item a wall holds.
    pub fn of<'a>(kinds: impl IntoIterator<Item = &'a str>) -> Self {
        let mut counts = Self::default();
        for kind in kinds {
            counts.items += 1;
            match kind {
                "movies" => counts.movies += 1,
                "series" => counts.series += 1,
                _ => {}
            }
        }
        counts
    }

    // The counts by kind as one phrase. A kind with no items is left
    // out, and a wall of neither kind reads as its item count.
    fn words(&self) -> String {
        let mut words = Vec::new();
        if self.movies > 0 {
            words.push(format!("{} {}", self.movies, noun(self.movies, "movie")));
        }
        if self.series > 0 {
            words.push(format!("{} series", self.series));
        }
        match words.is_empty() {
            true => self.items.to_string(),
            false => words.join(", "),
        }
    }
}

// The noun a count takes, singular at one.
fn noun(count: usize, singular: &str) -> String {
    match count {
        1 => singular.to_string(),
        _ => format!("{singular}s"),
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
    /// The tagline the sidecar wrote, empty where the read carried none.
    /// A film's card leads with it.
    pub tagline: String,
    pub parts: String,
    pub episode: Option<InSeries>,
    /// How many of the episodes a folded show holds are current, and
    /// zero on every other slot.
    pub new: usize,
    /// How many seasons a series slot's episodes fall into. Zero on every
    /// other kind, and where the read carried none.
    pub seasons: i64,
    /// How far a play of the work reached, on the slots of the
    /// continue-watching row and on episodes with a play behind them.
    /// Nothing on every other slot.
    pub progress: Option<Played>,
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
            tagline: title.tagline,
            parts: String::new(),
            episode: None,
            new: 0,
            seasons: 0,
            progress: None,
        }
    }

    /// Whether the slot's art is a still at 16:9 and not a poster at 2:3.
    /// Only an episode's is.
    pub fn still(&self) -> bool {
        self.episode.is_some()
    }

    /// Whether the slot is a whole show folded into one still, and not
    /// one episode of it: its id is then its series' id.
    pub fn folded(&self) -> bool {
        self.episode
            .as_ref()
            .is_some_and(|place| place.series == self.id)
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
mod tests;
