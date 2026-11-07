// The last answer of the progress read, kept until either file changes.
// A franchise page reads the progress of the room and then of each person
// in it, and every wall and strip reads it again on each open, so without
// the copy each of those reads runs the statement again for the same plays.
//
// The copy is checked against SQLite's `data_version` of both files. The
// number changes when another connection commits to the file, which here is
// the catalog agent or the progress agent, because this connection only
// reads. So the copy is dropped on the first read after any write, whether
// or not the update streams delivered that write's event.

use std::sync::Arc;

use rusqlite::Connection;

use crate::catalog::Seen;

pub(super) struct Kept {
    // The audience the copy was read for, sorted.
    people: Vec<String>,
    versions: Versions,
    pub seen: Arc<Vec<Seen>>,
}

// The `data_version` of the catalog file and of the attached progress file.
pub(super) type Versions = (i64, i64);

pub(super) fn versions(connection: &Connection) -> rusqlite::Result<Versions> {
    let catalog = connection.query_row("PRAGMA main.data_version", [], |row| row.get(0))?;
    let progress = connection.query_row("PRAGMA progress.data_version", [], |row| row.get(0))?;
    Ok((catalog, progress))
}

impl Kept {
    pub fn new(people: &[String], versions: Versions, seen: Vec<Seen>) -> Self {
        Self {
            people: sorted(people),
            versions,
            seen: Arc::new(seen),
        }
    }

    /// Whether the copy answers this audience at these versions. A copy
    /// read for a larger audience holds every play of a smaller one inside
    /// it, because the read takes each play some person of the audience is
    /// on. An empty audience reads the plays that name nobody, which no
    /// other audience's copy holds.
    pub fn answers(&self, people: &[String], versions: Versions) -> bool {
        if self.versions != versions {
            return false;
        }
        match (self.people.is_empty(), people.is_empty()) {
            (true, true) => true,
            (false, false) => people
                .iter()
                .all(|person| self.people.binary_search(person).is_ok()),
            _ => false,
        }
    }
}

fn sorted(people: &[String]) -> Vec<String> {
    let mut people = people.to_vec();
    people.sort();
    people.dedup();
    people
}

#[cfg(test)]
mod tests {
    use super::*;

    fn names(people: &[&str]) -> Vec<String> {
        people.iter().map(|name| name.to_string()).collect()
    }

    #[test]
    fn a_copy_answers_its_own_audience_and_any_part_of_it() {
        let kept = Kept::new(&names(&["second", "first"]), (1, 1), Vec::new());

        assert!(kept.answers(&names(&["first", "second"]), (1, 1)));
        assert!(kept.answers(&names(&["second"]), (1, 1)));
        assert!(!kept.answers(&names(&["third"]), (1, 1)));
        assert!(!kept.answers(&[], (1, 1)));
    }

    #[test]
    fn a_copy_answers_nothing_after_either_file_changes() {
        let kept = Kept::new(&names(&["first"]), (1, 1), Vec::new());

        assert!(!kept.answers(&names(&["first"]), (2, 1)));
        assert!(!kept.answers(&names(&["first"]), (1, 2)));
    }

    #[test]
    fn a_copy_for_nobody_answers_only_nobody() {
        let kept = Kept::new(&[], (1, 1), Vec::new());

        assert!(kept.answers(&[], (1, 1)));
        assert!(!kept.answers(&names(&["first"]), (1, 1)));
    }
}
