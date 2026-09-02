// A deterministic invented catalog, so the binary browses something
// before the sidecar source lands. Every name here is synthesized; nothing
// resembles a real library.

use iced_widget::image::Handle;

use crate::catalog::{
    Credit, EpisodeRow, LibraryEntry, MovieDetails, MovieSet, PlayItem, Selection, Source, Title,
};
use crate::harness::Waker;
use crate::posters::Posters;

// Enough movies to exercise the wall's culling, near the
// head-to-head's five thousand.
const MOVIES: i64 = 2987;

// A small series library, enough to walk every level.
const SERIALS: i64 = 12;

// How many of the first movies belong to a set, and how many members a
// set holds. The sets sit at the front of the wall, so a headless run
// reaches a page with a strip in two presses.
const IN_SETS: i64 = 12;
const PER_SET: i64 = 3;

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
            (1..=MOVIES).map(movie).collect()
        } else {
            (1..=SERIALS)
                .map(|number| Title {
                    id: format!("series:sample:{number:02}"),
                    title: format!("Serial {number:02}"),
                    released: (1960 + number * 5).to_string(),
                    art: format!("art/serial-{number:02}.jpg"),
                    duration: 0,
                    rating: "TV-14".into(),
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

    // The invented details of one movie. Every field is a function of the
    // movie's number, so a run draws the same page every time.
    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        let number = trailing(id);
        if !(1..=MOVIES).contains(&number) {
            return None;
        }
        let title = movie(number);
        Some(MovieDetails {
            title: title.title,
            released: title.released,
            duration: title.duration,
            rating: title.rating,
            genres: vec!["Drama".into(), "Mystery".into()],
            tagline: format!("The {number:04}th of its kind."),
            plot: PLOT.repeat(2),
            directors: vec![format!("Director {number:04}")],
            writers: vec![format!("Writer {number:04}"), "A Second Writer".into()],
            cast: (1..=6)
                .map(|part| Credit {
                    name: format!("Player {number:04}-{part}"),
                    role: format!("Part {part}"),
                })
                .collect(),
            set_id: set_of(number),
            backdrop: format!("backdrops/specimen-{number:04}.jpg"),
            logo: String::new(),
            trailer: format!("Specimen {number:04}/trailer.mkv"),
        })
    }

    fn set(&mut self, _library: &str, id: &str) -> Option<MovieSet> {
        let set = trailing(id);
        let mut members: Vec<Title> = (1..=IN_SETS)
            .filter(|number| set_of(*number) == format!("set:sample:{set:02}"))
            .map(movie)
            .collect();
        if members.is_empty() {
            return None;
        }
        // The catalog answers a set in release order, so the sample
        // answers in that order too.
        members.sort_by(|one, other| (&one.released, &one.id).cmp(&(&other.released, &other.id)));
        Some(MovieSet {
            title: format!("The Specimen Cycle {set:02}"),
            members,
        })
    }

    // The sample invents titles and no files, so a select on one starts
    // nothing. A workstation run browses and plays nothing.
    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

// The invented plot, long enough that the page cuts it and draws the
// fade.
const PLOT: &str = "A survey party reaches the coppice at dusk and finds the ground already \
     turned. What they take for a season of quiet work becomes a study of the \
     people who left the marks, and of the reason the marks were left at all. ";

// One invented movie, the same row every time.
fn movie(number: i64) -> Title {
    Title {
        id: format!("movie:sample:{number:04}"),
        title: format!("Specimen {number:04}"),
        released: (1900 + (number * 37) % 126).to_string(),
        art: format!("posters/specimen-{number:04}.jpg"),
        duration: 4_800 + (number % 47) * 60,
        rating: "PG-13".into(),
    }
}

// The set a movie belongs to. The first movies fall into sets of three,
// and every movie after them belongs to none.
fn set_of(number: i64) -> String {
    if number > IN_SETS {
        return String::new();
    }
    format!("set:sample:{:02}", (number - 1) / PER_SET + 1)
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
    fn a_select_on_the_sample_resolves_no_file() {
        let mut catalog = Catalog;
        assert!(
            catalog
                .play(
                    "sample/features",
                    &Selection::Movie {
                        id: "movie:sample:0001".into()
                    }
                )
                .is_empty()
        );
    }

    #[test]
    fn a_movie_carries_a_page_with_a_backdrop_and_a_trailer() {
        let mut catalog = Catalog;
        let details = catalog
            .movie("sample/features", "movie:sample:0001")
            .expect("the sample holds this movie");
        assert_eq!(details.title, "Specimen 0001");
        assert!(!details.backdrop.is_empty());
        assert!(!details.trailer.is_empty());
        assert_eq!(details.cast.len(), 6);
        assert!(details.duration > 0);
    }

    #[test]
    fn a_movie_the_sample_never_invented_has_no_page() {
        let mut catalog = Catalog;
        assert_eq!(catalog.movie("sample/features", "movie:sample:9999"), None);
        assert_eq!(catalog.movie("sample/features", "nonsense"), None);
    }

    #[test]
    fn the_first_movies_fall_into_sets_and_the_rest_into_none() {
        let mut catalog = Catalog;
        let first = catalog
            .movie("sample/features", "movie:sample:0001")
            .expect("the sample holds this movie");
        assert_eq!(first.set_id, "set:sample:01");
        let later = catalog
            .movie(
                "sample/features",
                &format!("movie:sample:{:04}", IN_SETS + 1),
            )
            .expect("the sample holds this movie");
        assert!(later.set_id.is_empty());
    }

    #[test]
    fn a_set_holds_its_own_members_in_order() {
        let mut catalog = Catalog;
        let set = catalog
            .set("sample/features", "set:sample:01")
            .expect("the sample holds this set");
        let ids: Vec<&str> = set
            .members
            .iter()
            .map(|member| member.id.as_str())
            .collect();
        assert_eq!(
            ids,
            [
                "movie:sample:0001",
                "movie:sample:0002",
                "movie:sample:0003"
            ]
        );
        assert_eq!(set.members.len() as i64, PER_SET);
        assert!(set.title.starts_with("The Specimen Cycle"));
    }

    #[test]
    fn a_set_the_sample_never_invented_is_empty() {
        assert_eq!(Catalog.set("sample/features", "set:sample:77"), None);
    }

    #[test]
    fn no_art_answers_none() {
        assert_eq!(
            NoArt.poster("sample/features", "posters/x.jpg", 10, 15),
            None
        );
    }
}
