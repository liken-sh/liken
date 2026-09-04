// Every screen of titles is one Query and one wall. A Query is a value
// of a closed set of shapes. Each shape has a heading a person reads, an
// order, and a read the catalog answers from an index, and none is a
// string of SQL: a string cannot be named in a heading and cannot promise
// an indexed read. This module holds the Query, the Slot a read answers
// with, and the Answer that names what the query is about.

use super::{Title, library_name};

/// The queries a wall can be fed. The set is closed and grows by one
/// variant per plan. `Library` is one library in sort order. `Person` is
/// every work of one person across the libraries. `Set` is the members of
/// one set in release order.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Query {
    Library { library: String },
    Person { library: String, path: String },
    Set { library: String, id: String },
}

impl Query {
    /// The heading the band draws over this query's slots. A library's
    /// heading carries its count. A person's and a set's carry the name
    /// alone, and that name comes with the answer, because only the catalog
    /// holds it.
    pub fn heading(&self, name: &str, count: usize) -> String {
        match self {
            Self::Library { library } => format!("{} · {count}", library_name(library)),
            Self::Person { .. } | Self::Set { .. } => name.to_string(),
        }
    }
}

/// One title as a read answers it. Every slot carries its own library
/// and kind, because no wall fixes up front what a select opens, and a
/// person's works span libraries. `parts` is empty on every read but a
/// person's.
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
        }
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
    }
}
