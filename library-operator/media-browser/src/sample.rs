// A deterministic invented catalog, so the binary browses something
// before the sidecar source lands. Every name here is synthesized; nothing
// resembles a real library.

use iced_widget::image::Handle;

use crate::catalog::{EpisodeRow, LibraryEntry, Source, Title};
use crate::harness::Waker;
use crate::posters::Posters;

// Enough movies to exercise the wall's culling, near the
// head-to-head's five thousand.
const MOVIES: i64 = 2987;

// A small series library, enough to walk every level.
const SERIALS: i64 = 12;

/// The invented catalog. It holds no state; every answer is a
/// function of its arguments.
#[derive(Debug, Default)]
pub struct Catalog;

impl Source for Catalog {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        vec![
            LibraryEntry {
                library: "sample/features".into(),
                kind: "movies".into(),
                items: MOVIES as u64,
            },
            LibraryEntry {
                library: "sample/serials".into(),
                kind: "series".into(),
                items: SERIALS as u64,
            },
        ]
    }

    fn titles(&mut self, _library: &str, kind: &str) -> Vec<Title> {
        if kind == "movies" {
            (1..=MOVIES)
                .map(|number| Title {
                    id: format!("movie:sample:{number:04}"),
                    title: format!("Specimen {number:04}"),
                    released: (1900 + (number * 37) % 126).to_string(),
                    art: format!("posters/specimen-{number:04}.jpg"),
                })
                .collect()
        } else {
            (1..=SERIALS)
                .map(|number| Title {
                    id: format!("series:sample:{number:02}"),
                    title: format!("Serial {number:02}"),
                    released: (1960 + number * 5).to_string(),
                    art: format!("art/serial-{number:02}.jpg"),
                })
                .collect()
        }
    }

    fn seasons(&mut self, _library: &str, series: &str) -> Vec<i64> {
        (1..=2 + trailing(series) % 3).collect()
    }

    fn episodes(&mut self, _library: &str, series: &str, season: i64) -> Vec<EpisodeRow> {
        (1..=6 + (trailing(series) + season) % 5)
            .map(|episode| EpisodeRow {
                id: format!(
                    "episode:sample:{:02}:{season:02}:{episode:02}",
                    trailing(series)
                ),
                title: format!("Segment {episode:02}"),
                season,
                episode,
                art: String::new(),
            })
            .collect()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

// The digits at the end of a sample id seed that item's structure,
// so every serial gets its own season and episode counts and gets the same
// ones on every run.
fn trailing(id: &str) -> i64 {
    id.rsplit(':')
        .next()
        .and_then(|digits| digits.parse().ok())
        .unwrap_or(0)
}

/// A poster store with nothing in it, so every slot draws the
/// placeholder until the real store lands.
#[derive(Debug, Default)]
pub struct NoArt;

impl Posters for NoArt {
    fn poster(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Handle> {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_counts_match_the_entries() {
        let mut catalog = Catalog;
        let libraries = catalog.libraries();
        let movies = catalog.titles("sample/features", "movies");
        let serials = catalog.titles("sample/serials", "series");
        assert_eq!(movies.len() as u64, libraries[0].items);
        assert_eq!(serials.len() as u64, libraries[1].items);
    }

    #[test]
    fn the_catalog_is_deterministic() {
        let mut catalog = Catalog;
        assert_eq!(
            catalog.titles("sample/features", "movies"),
            catalog.titles("sample/features", "movies")
        );
        assert_eq!(
            catalog.episodes("sample/serials", "series:sample:03", 2),
            catalog.episodes("sample/serials", "series:sample:03", 2)
        );
    }

    #[test]
    fn every_name_is_invented() {
        let mut catalog = Catalog;
        let movies = catalog.titles("sample/features", "movies");
        assert!(
            movies
                .iter()
                .all(|title| title.title.starts_with("Specimen "))
        );
        let serials = catalog.titles("sample/serials", "series");
        assert!(
            serials
                .iter()
                .all(|title| title.title.starts_with("Serial "))
        );
    }

    #[test]
    fn every_serial_has_seasons_with_episodes() {
        let mut catalog = Catalog;
        let seasons = catalog.seasons("sample/serials", "series:sample:07");
        assert_eq!(seasons, vec![1, 2, 3]);
        let episodes = catalog.episodes("sample/serials", "series:sample:07", 1);
        assert_eq!(episodes.len(), 9);
        assert_eq!(episodes[0].season, 1);
        assert_eq!(episodes[0].episode, 1);
    }

    #[test]
    fn the_sample_reports_no_changes() {
        assert!(!Catalog.changed());
    }

    #[test]
    fn no_art_answers_none() {
        assert_eq!(
            NoArt.poster("sample/features", "posters/x.jpg", 10, 15),
            None
        );
    }
}
