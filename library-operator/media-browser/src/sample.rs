// A deterministic invented catalog, so the binary browses something
// before the sidecar source lands. Every name here is synthesized; nothing
// resembles a real library.

use crate::catalog::franchise;
use crate::catalog::recency::{self, Candidate};
use crate::catalog::{
    Answer, Credit, Credits, Episode, FileFacts, Franchise, FranchiseEntry, GenreEntry,
    LibraryEntry, Membership, MovieDetails, MovieSet, Person, PlayItem, Query, Selection,
    SeriesDetails, Slot, Source, TILES, Title, library_name, pool,
};
use crate::harness::Waker;
use crate::posters::{Art, Posters};

// The invented people of the sample catalog.
pub mod people;

// The invented genres and the invented pool the day's draw reads.
mod draw;

// The invented franchises: the two orders the strip and the franchise
// page draw.
mod orders;

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
                art: newest_movies(),
            },
            LibraryEntry {
                library: SERIALS_LIBRARY.into(),
                kind: "series".into(),
                items: SERIALS as u64,
                // A serial's import day climbs with its number, so the
                // newest-added serials are the last ones invented.
                art: (0..TILES as i64)
                    .map(|step| serial(SERIALS - step).art)
                    .collect(),
            },
        ]
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        draw::genres()
    }

    fn franchises(&mut self) -> Vec<FranchiseEntry> {
        orders::entries()
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
                slots: recency::filled(
                    *fold,
                    recent(|candidate| released_of(candidate).to_string()),
                ),
            },
            Query::Added { fold } => Answer {
                name: String::new(),
                slots: recency::filled(*fold, recent(added_of)),
            },
            Query::Genre { name, order } => Answer {
                name: name.clone(),
                slots: draw::titles(name, *order),
            },
            Query::Franchise { library, id } => franchise::answer(self.franchise(library, id)),
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
            tagline: serial(number).tagline,
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
            tagline: tagline(number),
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

    fn franchises_of(&mut self, _library: &str, id: &str) -> Vec<Membership> {
        orders::memberships(id)
    }

    fn franchise(&mut self, library: &str, id: &str) -> Option<Franchise> {
        orders::franchise(library, id)
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
        .map(|number| serial_slot(library, number))
        .collect()
}

// The tagline of one invented movie. Every third movie has none, so a
// card of one falls back to its title.
pub(super) fn tagline(number: i64) -> String {
    match number % 3 == 0 {
        true => String::new(),
        false => format!("The {number:04}th of its kind."),
    }
}

// One invented serial as a slot, with the season count the sidecar's
// series read carries.
pub(super) fn serial_slot(library: &str, number: i64) -> Slot {
    Slot {
        seasons: seasons(number),
        ..Slot::of(library, "series", serial(number))
    }
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
        tagline: format!("Serial {number:02}, in its own seasons."),
    }
}

// The year an invented serial started. The last serial starts in the
// year the newest movies came out, so its seasons air after them and the
// home page's strips show episodes.
fn serial_year(number: i64) -> i64 {
    1965 + number * 5
}

// The recency candidates: every movie and every episode with the
// arrival the sample invents for it, sorted newest first by the key. The
// paging and the fold over them are the catalog's own.
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
                    tagline: String::new(),
                },
                series: serial(number),
            });
        }
    }
    candidates.sort_by_key(|candidate| std::cmp::Reverse(key(candidate)));
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

// The posters of the newest-added invented movies, which the libraries
// strip draws the features library as, in the order the Added query
// answers them.
fn newest_movies() -> Vec<String> {
    let mut numbers: Vec<i64> = (1..=MOVIES).collect();
    numbers.sort_by_key(|number| std::cmp::Reverse(movie_arrival(*number)));
    numbers
        .into_iter()
        .take(TILES)
        .map(|number| movie(number).art)
        .collect()
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
        tagline: tagline(number),
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
mod tests;
