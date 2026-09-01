// The seam between the catalog and the views. The views draw rows, and a
// `Source` yields them, so one set of views draws the sidecar's file, a
// test fixture, and the sample data the same way.

use crate::harness::Waker;

// The sidecar module implements this seam over plan 06's delivery: a
// read-only open of the sidecar's file, and its update stream.
pub mod sidecar;

/// One library, as the first screen lists it: the name, the kind, and the
/// count of items it holds.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LibraryEntry {
    /// The catalog's `library` column: the `Library`'s namespace and name,
    /// joined as `namespace/name`.
    pub library: String,
    /// The `Library`'s kind, `movies` or `series`. The kind names the item
    /// table the titles come from, so the views carry it back into every
    /// later call for this library.
    pub kind: String,
    /// How many items the library holds.
    pub items: u64,
}

/// One title in a kind's top list: a movie, or a series.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Title {
    /// The item's provider-scoped id, unique inside its library.
    pub id: String,
    /// The name a person reads.
    pub title: String,
    /// The year or the date of release, as the catalog stores it.
    pub released: String,
    /// The path of the primary art, relative to the library root, or empty
    /// where the item has none.
    pub art: String,
}

/// One episode under a season.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EpisodeRow {
    /// The episode's provider-scoped id, unique inside its library.
    pub id: String,
    /// The name a person reads.
    pub title: String,
    /// The aired season number that places the episode.
    pub season: i64,
    /// The aired episode number inside the season.
    pub episode: i64,
    /// The path of the episode's art, relative to the library root, or
    /// empty where it has none.
    pub art: String,
}

/// What the views read. Every list comes back in the order the views draw
/// it, so the views sort nothing: titles by the scanner's sort key, and
/// seasons and episodes by their aired numbers.
///
/// Every method reads local state and returns at once; no call waits on a
/// network. [`Source::changed`] carries the freshness contract from plan
/// 06: a source with an update stream folds events in behind these calls,
/// wakes the loop through the handle from [`Source::wake_by`], and answers
/// true once, and the views then re-read what they show.
pub trait Source {
    /// Every library in the catalog, ordered by name. Which libraries a
    /// screen shows is an open problem, so until that resource exists the
    /// first screen lists them all.
    fn libraries(&mut self) -> Vec<LibraryEntry>;

    /// One library's top list, ordered by sort key: its movies for the
    /// kind `movies`, its series for the kind `series`.
    fn titles(&mut self, library: &str, kind: &str) -> Vec<Title>;

    /// The seasons one series has episodes for, in order.
    fn seasons(&mut self, library: &str, series: &str) -> Vec<i64>;

    /// One season's episodes, in order.
    fn episodes(&mut self, library: &str, series: &str, season: i64) -> Vec<EpisodeRow>;

    /// The play list one choice resolves to. A movie is one item or
    /// none. An episode is itself and every later episode of its season,
    /// in episode order. A choice whose own main file is missing
    /// resolves to nothing, because a play that skipped what the person
    /// chose is worse than no play at all.
    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem>;

    /// Whether anything changed since the last call.
    fn changed(&mut self) -> bool;

    /// Take the handle that wakes the loop, for a source with a stream of
    /// its own. A source with no stream takes it and does nothing.
    fn wake_by(&mut self, wake: Waker);
}

/// What a person chose on the wall, as the two things that resolve to a
/// play list: a movie by its id, and an episode by its series, season,
/// and aired number. The library is not in here because every read
/// takes it beside the choice.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Selection {
    /// One movie, named by its provider-scoped id.
    Movie {
        /// The movie's id inside its library.
        id: String,
    },
    /// One episode, and with it the rest of its season.
    Episode {
        /// The parent series' id inside the library.
        series: String,
        /// The aired season number.
        season: i64,
        /// The aired episode number the person chose.
        episode: i64,
    },
}

impl Selection {
    /// The choice as one line, for the log line a resolve that found
    /// nothing writes.
    pub fn named(&self) -> String {
        match self {
            Self::Movie { id } => id.clone(),
            Self::Episode {
                series,
                season,
                episode,
            } => format!("{series} S{season}E{episode}"),
        }
    }
}

/// One item of a play list: the main file's path relative to the
/// library root, and the words the film's own display shows.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PlayItem {
    /// The main file's path, relative to the library root.
    pub path: String,
    /// The presentation the operator passes through to the `Play`.
    pub presentation: Presentation,
}

/// media-operator's own presentation block, as the catalog answers it.
/// Every empty field is left out of the request, so this type carries
/// the same absences the JSON does.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Presentation {
    /// The medium, `video` for everything this plan resolves. It is
    /// `type` in the JSON, which Rust reserves.
    pub kind: String,
    /// What the item is, `movie` or `series`.
    pub hint: String,
    /// The movie's title. An episode carries none.
    pub title: String,
    /// The series' title, from the series row.
    pub series: String,
    /// The aired season number.
    pub season: i64,
    /// The aired episode number.
    pub episode: i64,
    /// The episode's own title.
    pub episode_title: String,
    /// The year of release, the first four digits of the catalog's
    /// released column.
    pub year: i64,
    /// The full ISO date of release, where the catalog holds one.
    pub date: String,
    /// The art path, relative to the library root.
    pub art: String,
    /// The trickplay path, relative to the library root.
    pub trickplay: String,
}
