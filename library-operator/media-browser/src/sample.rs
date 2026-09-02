// A deterministic invented catalog, so the binary browses something
// before the sidecar source lands. Every name here is synthesized; nothing
// resembles a real library.

use crate::catalog::{
    Credit, Episode, LibraryEntry, MovieDetails, MovieSet, PlayItem, Selection, SeriesDetails,
    Source, Title,
};
use crate::harness::Waker;
use crate::posters::{Art, Posters};

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

    // The invented details of one series, so a run with no catalog opens
    // a series page with a header, dividers, and stills.
    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        let number = trailing(id);
        if !(1..=SERIALS).contains(&number) {
            return None;
        }
        Some(SeriesDetails {
            title: format!("Serial {number:02}"),
            released: (1960 + number * 5).to_string(),
            duration: 0,
            rating: "TV-14".into(),
            genres: vec!["Drama".into(), "Mystery".into()],
            tagline: format!("Serial {number:02}, in its own seasons."),
            plot: PLOT.repeat(2),
            creators: vec![format!("Creator {number:02}")],
            cast: (1..=6)
                .map(|part| Credit {
                    name: format!("Player {number:02}-{part}"),
                    role: format!("Part {part}"),
                })
                .collect(),
            backdrop: format!("backdrops/serial-{number:02}.jpg"),
            logo: String::new(),
            seasons: seasons(number),
        })
    }

    fn episodes(&mut self, _library: &str, id: &str) -> Vec<Episode> {
        let number = trailing(id);
        if !(1..=SERIALS).contains(&number) {
            return Vec::new();
        }
        (1..=seasons(number))
            .flat_map(|season| {
                (1..=6 + (number + season) % 5).map(move |episode| Episode {
                    season,
                    episode,
                    title: format!("Segment {episode:02}"),
                    released: format!(
                        "{}-{:02}-{:02}",
                        1960 + number * 5 + season - 1,
                        1 + episode % 12,
                        1 + episode % 28
                    ),
                    duration: 2_400 + (episode % 7) * 60,
                    plot: format!("Segment {episode:02} of season {season}. {PLOT}"),
                    art: format!("stills/serial-{number:02}-s{season:02}e{episode:02}.jpg"),
                })
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

// The invented plot, long enough that a page cuts it at its last line.
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

// How many seasons an invented serial holds.
fn seasons(number: i64) -> i64 {
    2 + number % 3
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
    fn poster(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Art> {
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
            catalog.episodes("sample/serials", "series:sample:03"),
            catalog.episodes("sample/serials", "series:sample:03")
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
        let details = catalog
            .series("sample/serials", "series:sample:07")
            .expect("the sample holds this serial");
        assert_eq!(details.seasons, 3);
        assert!(!details.backdrop.is_empty());
        assert_eq!(details.cast.len(), 6);

        let episodes = catalog.episodes("sample/serials", "series:sample:07");
        assert_eq!(episodes.len(), 9 + 10 + 6);
        assert_eq!(episodes[0].season, 1);
        assert_eq!(episodes[0].episode, 1);
        assert_eq!(episodes[9].season, 2);
        assert!(!episodes[0].art.is_empty());
        assert!(!episodes[0].plot.is_empty());
        assert_eq!(episodes[0].released, "1995-02-02");
    }

    #[test]
    fn a_serial_the_sample_never_invented_has_no_page() {
        let mut catalog = Catalog;
        assert_eq!(catalog.series("sample/serials", "series:sample:99"), None);
        assert!(catalog.episodes("sample/serials", "nonsense").is_empty());
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
