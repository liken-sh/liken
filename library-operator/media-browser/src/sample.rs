// A deterministic invented catalog, so the binary browses something
// before the sidecar source lands. Every name here is synthesized; nothing
// resembles a real library.

use crate::catalog::recency::{self, CANDIDATES, Candidate};
use crate::catalog::{
    Answer, Credit, Credits, Episode, FileFacts, LibraryEntry, MovieDetails, MovieSet, Person,
    PlayItem, Query, Selection, SeriesDetails, Slot, Source, Title, library_name, pool,
};
use crate::harness::Waker;
use crate::posters::{Art, Posters};

// The invented people of the sample catalog.
mod people;

// The invented genres and the invented pool the day's draw reads.
mod draw;

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

// The two libraries' names, which every invented row carries.
const FEATURES: &str = "sample/features";
const SERIALS_LIBRARY: &str = "sample/serials";

// The invented arrivals. They count from one day and the movies fall
// over a spread of days, so the Added strip differs from the Released
// strip. The last three episodes of the last serial's last season arrive
// two days after each airs, so the home page shows standalone episodes
// beside a folded series and titles on one screen. Every other episode
// of a serial arrived on that serial's own import day, one step apart
// per serial, so its series folds. The last serial's import day is later
// than every movie's arrival and more than the fold window before its
// first episode of the season, so under Added the folded series comes
// right after the airing episodes.
const ARRIVALS_FROM: i64 = 1_640_995_200;
const ARRIVAL_SPREAD_DAYS: i64 = 1_400;
const AIRING_EPISODES: i64 = 3;
const AIRING_LAG_DAYS: i64 = 2;
const IMPORT_STEP_DAYS: i64 = 120;
const DAY: i64 = 86_400;

/// The invented catalog. It holds no state; every answer is a
/// function of its arguments.
#[derive(Debug, Default)]
pub struct Catalog;

impl Source for Catalog {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        vec![
            LibraryEntry {
                library: FEATURES.into(),
                kind: "movies".into(),
                items: MOVIES as u64,
                art: movie(1).art,
            },
            LibraryEntry {
                library: SERIALS_LIBRARY.into(),
                kind: "series".into(),
                items: SERIALS as u64,
                art: serial(SERIALS).art,
            },
        ]
    }

    fn wall(&mut self, query: &Query) -> Answer {
        match query {
            Query::Library { library } => Answer {
                name: library_name(library).to_string(),
                slots: titles(library),
            },
            Query::Person { library, path } => Answer {
                name: people::person(library, path)
                    .map(|person| person.name)
                    .unwrap_or_default(),
                slots: people::works(library, path),
            },
            Query::Set { library, id } => match self.set(library, id) {
                Some(set) => Answer {
                    name: set.title,
                    slots: set
                        .members
                        .into_iter()
                        .map(|member| Slot::of(library, "movies", member))
                        .collect(),
                },
                None => Answer::default(),
            },
            Query::Released { fold } => Answer {
                name: String::new(),
                slots: recency::fold(
                    recent(|candidate| released_of(candidate).to_string()),
                    *fold,
                ),
            },
            Query::Added { fold } => Answer {
                name: String::new(),
                slots: recency::fold(recent(added_of), *fold),
            },
            Query::Genre { name, order } => Answer {
                name: name.clone(),
                slots: draw::titles(name, *order),
            },
        }
    }

    fn pool(&mut self) -> Vec<pool::Candidate> {
        draw::pool()
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
            released: serial_year(number).to_string(),
            duration: 0,
            rating: "TV-14".into(),
            genres: draw::serial_genres(),
            tagline: format!("Serial {number:02}, in its own seasons."),
            plot: PLOT.repeat(2),
            creators: vec![format!("Creator {number:02}")],
            cast: (1..=6)
                .map(|part| Credit {
                    name: format!("Player {number:02}-{part}"),
                    role: format!("Part {part}"),
                })
                .collect(),
            studios: vec![format!("Studio {number:02}")],
            ratings: ratings(number),
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
                (1..=episodes_in(number, season)).map(move |episode| Episode {
                    id: format!("episode:sample:{number:02}-{season:02}-{episode:02}"),
                    season,
                    episode,
                    title: format!("Segment {episode:02}"),
                    released: format!(
                        "{}-{:02}-{:02}",
                        serial_year(number) + season - 1,
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
            genres: draw::movie_genres(number),
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
            studios: vec![format!("Studio {number:04}"), "A Second Studio".into()],
            ratings: ratings(number),
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

    fn credits(&mut self, _library: &str, id: &str) -> Credits {
        people::credits(id)
    }

    // The invented files of one title: the video the page draws a line
    // for, and two subtitle files beside it.
    fn files(&mut self, _library: &str, item: &str) -> Vec<FileFacts> {
        let number = trailing(item);
        if number == 0 {
            return Vec::new();
        }
        let mut files = vec![FileFacts {
            role: "primary".into(),
            kind: "video".into(),
            container: "mkv".into(),
            video_codec: "x265".into(),
            audio_codec: "AC3".into(),
            width: 1_920,
            height: 804,
            size_bytes: 4_200_000_000 + number * 1_000_000,
            language: String::new(),
        }];
        for language in ["en", "fr"] {
            files.push(FileFacts {
                role: "subtitle".into(),
                kind: "subtitle".into(),
                container: "srt".into(),
                language: language.into(),
                ..FileFacts::default()
            });
        }
        files
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Person> {
        people::person(library, path)
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

// One library's invented slots: the movies for the features library,
// and the serials for any other, each slot stamped with that library and
// its kind.
fn titles(library: &str) -> Vec<Slot> {
    if library == FEATURES {
        return (1..=MOVIES)
            .map(|number| Slot::of(library, "movies", movie(number)))
            .collect();
    }
    (1..=SERIALS)
        .map(|number| Slot::of(library, "series", serial(number)))
        .collect()
}

// One invented serial, as a title row.
fn serial(number: i64) -> Title {
    Title {
        id: format!("series:sample:{number:02}"),
        title: format!("Serial {number:02}"),
        released: serial_year(number).to_string(),
        art: format!("art/serial-{number:02}.jpg"),
        duration: 0,
        rating: "TV-14".into(),
    }
}

// The year an invented serial started. The last serial starts in the
// year the newest movies came out, so its seasons air after them and the
// home page's strips show episodes.
fn serial_year(number: i64) -> i64 {
    1965 + number * 5
}

// The recency candidates: every movie and every episode with the
// arrival the sample invents for it, sorted newest first by the key and
// bounded as the catalog's read is. The fold over them is the catalog's
// own.
fn recent<K: Ord>(key: impl Fn(&Candidate) -> K) -> Vec<Candidate> {
    let mut catalog = Catalog;
    let mut candidates: Vec<Candidate> = (1..=MOVIES)
        .map(|number| Candidate::Movie {
            slot: Slot::of(FEATURES, "movies", movie(number)),
        })
        .collect();
    for number in 1..=SERIALS {
        for episode in catalog.episodes(SERIALS_LIBRARY, &serial(number).id) {
            candidates.push(Candidate::Episode {
                library: SERIALS_LIBRARY.into(),
                added: arrival(number, &episode),
                season: episode.season,
                number: episode.episode,
                episode: Title {
                    id: episode.id,
                    title: episode.title,
                    released: episode.released,
                    art: episode.art,
                    duration: episode.duration,
                    rating: String::new(),
                },
                series: serial(number),
            });
        }
    }
    candidates.sort_by_key(|candidate| std::cmp::Reverse(key(candidate)));
    candidates.truncate(CANDIDATES);
    candidates
}

fn released_of(candidate: &Candidate) -> &str {
    match candidate {
        Candidate::Movie { slot } => &slot.released,
        Candidate::Episode { episode, .. } => &episode.released,
    }
}

fn added_of(candidate: &Candidate) -> i64 {
    match candidate {
        Candidate::Movie { slot } => movie_arrival(trailing(&slot.id)),
        Candidate::Episode { added, .. } => *added,
    }
}

// The invented arrival of one movie, spread over the years after the
// first arrival day.
fn movie_arrival(number: i64) -> i64 {
    ARRIVALS_FROM + (number * 7_919) % ARRIVAL_SPREAD_DAYS * DAY
}

// The invented arrival of one episode: two days after it aired for the
// last episodes of the last serial's last season, and the serial's own
// import day everywhere else.
fn arrival(number: i64, episode: &Episode) -> i64 {
    let season = seasons(number);
    let airing = number == SERIALS
        && episode.season == season
        && episode.episode + AIRING_EPISODES > episodes_in(number, season);
    match airing {
        true => recency::date_seconds(&episode.released).unwrap_or(0) + AIRING_LAG_DAYS * DAY,
        false => ARRIVALS_FROM + number * IMPORT_STEP_DAYS * DAY,
    }
}

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

// The invented scores of one title, on the three sites the page draws
// and on TMDb, which it leaves off.
fn ratings(number: i64) -> Vec<(String, f64)> {
    [
        ("imdb", 5.0 + (number % 50) as f64 / 10.0),
        ("metacritic", (30 + number % 70) as f64),
        ("themoviedb", 6.0 + (number % 40) as f64 / 10.0),
        ("tomatometerallcritics", (20 + number % 80) as f64),
    ]
    .into_iter()
    .map(|(name, score)| (name.to_string(), score))
    .collect()
}

// How many seasons an invented serial holds.
fn seasons(number: i64) -> i64 {
    2 + number % 3
}

// How many episodes one season of an invented serial holds.
fn episodes_in(number: i64, season: i64) -> i64 {
    6 + (number + season) % 5
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

    fn library(name: &str) -> Query {
        Query::Library {
            library: name.into(),
        }
    }

    #[test]
    fn the_counts_match_the_entries() {
        let mut catalog = Catalog;
        let libraries = catalog.libraries();
        let movies = catalog.wall(&library("sample/features"));
        let serials = catalog.wall(&library("sample/serials"));
        assert_eq!(movies.slots.len() as u64, libraries[0].items);
        assert_eq!(serials.slots.len() as u64, libraries[1].items);
        assert_eq!(movies.name, "features");
        assert_eq!(serials.name, "serials");
    }

    #[test]
    fn every_slot_of_a_library_wall_names_its_library_and_kind() {
        let mut catalog = Catalog;
        let movies = catalog.wall(&library("sample/features")).slots;
        assert!(
            movies
                .iter()
                .all(|slot| slot.library == "sample/features" && slot.kind == "movies")
        );
        let serials = catalog.wall(&library("sample/serials")).slots;
        assert!(
            serials
                .iter()
                .all(|slot| slot.library == "sample/serials" && slot.kind == "series")
        );
    }

    #[test]
    fn a_persons_wall_is_headed_by_their_name() {
        let mut catalog = Catalog;
        let answer = catalog.wall(&Query::Person {
            library: "sample/features".into(),
            path: ".contributors/Player 0001-1".into(),
        });
        assert_eq!(answer.name, "Player 0001-1");
        assert_eq!(answer.slots.len(), 3);
        assert_eq!(
            catalog.wall(&Query::Person {
                library: "sample/features".into(),
                path: "nonsense".into(),
            }),
            Answer::default()
        );
    }

    #[test]
    fn a_sets_wall_is_headed_by_its_title_and_holds_its_members() {
        let mut catalog = Catalog;
        let answer = catalog.wall(&Query::Set {
            library: "sample/features".into(),
            id: "set:sample:01".into(),
        });
        assert!(answer.name.starts_with("The Specimen Cycle"));
        assert_eq!(answer.slots.len() as i64, PER_SET);
        assert_eq!(answer.slots[0].id, "movie:sample:0001");
        assert_eq!(answer.slots[0].kind, "movies");
        assert_eq!(
            catalog.wall(&Query::Set {
                library: "sample/features".into(),
                id: "set:sample:77".into(),
            }),
            Answer::default()
        );
    }

    #[test]
    fn the_catalog_is_deterministic() {
        let mut catalog = Catalog;
        assert_eq!(
            catalog.wall(&library("sample/features")),
            catalog.wall(&library("sample/features"))
        );
        assert_eq!(
            catalog.episodes("sample/serials", "series:sample:03"),
            catalog.episodes("sample/serials", "series:sample:03")
        );
    }

    #[test]
    fn every_name_is_invented() {
        let mut catalog = Catalog;
        let movies = catalog.wall(&library("sample/features")).slots;
        assert!(
            movies
                .iter()
                .all(|slot| slot.title.starts_with("Specimen "))
        );
        let serials = catalog.wall(&library("sample/serials")).slots;
        assert!(serials.iter().all(|slot| slot.title.starts_with("Serial ")));
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
        assert_eq!(episodes[0].released, "2000-02-02");
    }

    #[test]
    fn the_home_strips_show_a_standalone_episode_a_folded_series_and_titles() {
        let mut catalog = Catalog;
        for query in [
            Query::Released {
                fold: crate::catalog::Fold::Airing,
            },
            Query::Added {
                fold: crate::catalog::Fold::Airing,
            },
        ] {
            let slots = catalog.wall(&query).slots;
            assert!(slots.len() <= CANDIDATES);
            assert!(slots.iter().any(|slot| slot.still()));
            assert!(slots.iter().any(|slot| slot.kind == "series"));
            assert!(slots.iter().any(|slot| slot.kind == "movies"));
            assert_eq!(slots[0].kind, "episodes");
            assert_eq!(slots[0].library, SERIALS_LIBRARY);
            let stills = slots.iter().filter(|slot| slot.still()).count();
            assert_eq!(stills as i64, AIRING_EPISODES);
            assert_eq!(slots[AIRING_EPISODES as usize].kind, "series");
        }
    }

    #[test]
    fn released_comes_newest_first_and_added_by_arrival() {
        let mut catalog = Catalog;
        let released = catalog
            .wall(&Query::Released {
                fold: crate::catalog::Fold::Episodes,
            })
            .slots;
        assert!(
            released
                .windows(2)
                .all(|pair| pair[0].released >= pair[1].released)
        );
        let added = catalog
            .wall(&Query::Added {
                fold: crate::catalog::Fold::Titles,
            })
            .slots;
        assert!(added.iter().all(|slot| !slot.still()));
        assert_ne!(
            released[0].id,
            added.last().expect("the strip holds slots").id
        );
    }

    #[test]
    fn every_library_draws_as_one_of_its_posters() {
        let mut catalog = Catalog;
        assert!(
            catalog
                .libraries()
                .iter()
                .all(|entry| !entry.art.is_empty())
        );
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
