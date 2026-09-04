// The pool: every candidate strip the day may draw, as the query that
// reads it, the name a heading draws, and the weight the draw favors it
// by. A genre weighs the titles that carry it, with the ones that lead
// with it counted twice. A person weighs their works. A set weighs its
// members.

use super::Query;

/// The three kinds a candidate can be, so the draw takes no two of one
/// kind until every kind has been drawn once.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Kind {
    Genre,
    Person,
    Set,
}

/// One candidate of the pool: the query, the name its strip is headed
/// by, and its weight.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Candidate {
    pub query: Query,
    pub name: String,
    pub weight: u64,
}

impl Candidate {
    /// The candidate's kind. It comes off the query because only three
    /// query shapes enter the pool, and the shape says which.
    pub fn kind(&self) -> Kind {
        match self.query {
            Query::Person { .. } => Kind::Person,
            Query::Set { .. } => Kind::Set,
            _ => Kind::Genre,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Order;

    #[test]
    fn a_candidates_kind_is_its_querys_shape() {
        let genre = Candidate {
            query: Query::Genre {
                name: "Western".into(),
                order: Order::Released,
            },
            name: "Western".into(),
            weight: 5,
        };
        assert_eq!(genre.kind(), Kind::Genre);
        let person = Candidate {
            query: Query::Person {
                library: "screening/features".into(),
                path: ".contributors/A Player".into(),
            },
            name: "A Player".into(),
            weight: 4,
        };
        assert_eq!(person.kind(), Kind::Person);
        let set = Candidate {
            query: Query::Set {
                library: "screening/features".into(),
                id: "set:1".into(),
            },
            name: "The Cycle".into(),
            weight: 3,
        };
        assert_eq!(set.kind(), Kind::Set);
    }
}
