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
#[derive(Debug, Clone, Default, PartialEq, Eq)]
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
    /// The item's running time in seconds, zero where the catalog holds
    /// none.
    pub duration: i64,
    /// The content rating from the body, empty where the sidecar named
    /// none.
    pub rating: String,
}

/// One credited person and the part they played, from the body's cast.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Credit {
    /// The person's name.
    pub name: String,
    /// The part they played, empty where the sidecar named none.
    pub role: String,
}

/// What a movie's page draws: the item's own columns, the fields of its
/// body, and the three files the page reads by role.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct MovieDetails {
    /// The name a person reads.
    pub title: String,
    /// The year or the date of release, as the catalog stores it.
    pub released: String,
    /// The running time in seconds, zero where the catalog holds none.
    pub duration: i64,
    /// The content rating, empty where the sidecar named none.
    pub rating: String,
    /// The genres, in the order the sidecar named them.
    pub genres: Vec<String>,
    /// The one-line tagline, empty where the sidecar named none.
    pub tagline: String,
    /// The plot. The page cuts it to four lines.
    pub plot: String,
    /// The directors, in the order the sidecar named them.
    pub directors: Vec<String>,
    /// The writers, in the order the sidecar named them.
    pub writers: Vec<String>,
    /// The cast, in the order the sidecar named them.
    pub cast: Vec<Credit>,
    /// The studios, in the order the sidecar names them.
    pub studios: Vec<String>,
    /// Each site's score of the movie, keyed by the sidecar's own name for
    /// the site, on that site's own scale.
    pub ratings: Vec<(String, f64)>,
    /// The id of the set the movie belongs to, empty where it belongs to
    /// none.
    pub set_id: String,
    /// The path of the backdrop file, relative to the library root, or
    /// empty where the item has none.
    pub backdrop: String,
    /// The path of the logo file, relative to the library root, or empty
    /// where the item has none.
    pub logo: String,
    /// The path of the trailer file, relative to the library root, or
    /// empty where the item has none.
    pub trailer: String,
}

/// One set and every movie in it, in release order, as the strip on a
/// movie's page draws them.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct MovieSet {
    /// The set's own title. The strip draws it as its heading.
    pub title: String,
    /// The movies in the set, in release order.
    pub members: Vec<Title>,
}

/// The name half of a `library` column, `namespace/name`, which is the
/// half a screen draws.
pub fn library_name(library: &str) -> &str {
    library.split_once('/').map_or(library, |(_, name)| name)
}

/// What a series' page draws: the item's own columns, the fields of its
/// body, the two files it reads by role, and how many seasons its
/// episodes fall into.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct SeriesDetails {
    /// The name a person reads.
    pub title: String,
    /// The year or the date of release, as the catalog stores it.
    pub released: String,
    /// The running time in seconds, zero where the catalog holds none.
    pub duration: i64,
    /// The content rating, empty where the sidecar named none.
    pub rating: String,
    /// The genres, in the order the sidecar named them.
    pub genres: Vec<String>,
    /// The one-line tagline, empty where the sidecar named none.
    pub tagline: String,
    /// The plot. The page cuts it to two lines.
    pub plot: String,
    /// The creators, in the order the sidecar named them.
    pub creators: Vec<String>,
    /// The cast, in the order the sidecar named them.
    pub cast: Vec<Credit>,
    /// The studios, in the order the sidecar names them.
    pub studios: Vec<String>,
    /// Each site's score of the series, keyed by the sidecar's own name for
    /// the site, on that site's own scale.
    pub ratings: Vec<(String, f64)>,
    /// The path of the backdrop file, relative to the library root, or
    /// empty where the item has none.
    pub backdrop: String,
    /// The path of the logo file, relative to the library root, or empty
    /// where the item has none.
    pub logo: String,
    /// How many seasons the series' episodes fall into.
    pub seasons: i64,
}

/// One episode of a series, as one still of the series page's wall.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Episode {
    /// The episode's id inside its library, which its files are read by.
    pub id: String,
    /// The aired season number that places the episode.
    pub season: i64,
    /// The aired episode number inside the season.
    pub episode: i64,
    /// The name a person reads.
    pub title: String,
    /// The year or the date the episode aired, as the catalog stores it.
    pub released: String,
    /// The running time in seconds, zero where the catalog holds none.
    pub duration: i64,
    /// The plot, empty where the sidecar named none.
    pub plot: String,
    /// The path of the episode's still, relative to the library root, or
    /// empty where it has none.
    pub art: String,
}

/// One slot of a title's stripe: the person, what they did on this
/// title, and where their entry lives.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct CreditSlot {
    /// The name a person reads, as the title's own credits name it.
    pub name: String,
    /// The character an actor played, empty for the crew and for an
    /// actor the credits gave no role.
    pub role: String,
    /// The person's directory relative to the library volume, empty
    /// where the library's store holds no entry for them.
    pub contributor: String,
    /// Whether `headshot.jpg` is beside that entry.
    pub headshot: bool,
}

/// One file of a title, as the foot of a page reads it: what the file is,
/// how it is encoded, and how large it is.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct FileFacts {
    /// Which one of its kind the file is, such as `primary`.
    pub role: String,
    /// The file's category, such as `video` or `subtitle`.
    pub kind: String,
    /// The container the file is written in.
    pub container: String,
    /// The video codec, empty where the scanner read none.
    pub video_codec: String,
    /// The audio codec, empty where the scanner read none.
    pub audio_codec: String,
    /// The width in pixels, zero where the scanner read none.
    pub width: i64,
    /// The height in pixels, zero where the scanner read none.
    pub height: i64,
    /// The size in bytes, zero where the scanner read none.
    pub size_bytes: i64,
    /// The language tag the file name carries, empty where it carries none.
    pub language: String,
}

/// One title's credited people, split into the three stripes a page
/// draws, each in billing order.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Credits {
    /// The directors, in billing order.
    pub directors: Vec<CreditSlot>,
    /// The writers, in billing order.
    pub writers: Vec<CreditSlot>,
    /// The cast, in billing order.
    pub cast: Vec<CreditSlot>,
}

/// One person, as their own page draws them. `library` and `path` name
/// the entry the page opened from. The headshot and the biography can
/// each come from another library's entry for the same person, so the
/// four fields after the flags say which library and which directory
/// hold each file.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Person {
    /// The library the page opened from, as `namespace/name`.
    pub library: String,
    /// The person's directory in that library, relative to its
    /// volume.
    pub path: String,
    /// The name a person reads.
    pub name: String,
    /// The date of birth the entry holds, empty where it holds
    /// none.
    pub born: String,
    /// The date of death the entry holds, empty where it holds
    /// none.
    pub died: String,
    /// Whether any library holding this person has `biography.txt`
    /// beside the entry.
    pub biography: bool,
    /// Whether any library holding this person has `headshot.jpg`
    /// beside the entry.
    pub headshot: bool,
    /// The library whose entry holds the biography, empty where no
    /// library holds one.
    pub biography_library: String,
    /// The person's directory in that library.
    pub biography_path: String,
    /// The library whose entry holds the headshot, empty where no
    /// library holds one.
    pub headshot_library: String,
    /// The person's directory in that library.
    pub headshot_path: String,
}

/// One title a person is credited in, as one slot of their wall.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Work {
    /// The library that holds the title, as `namespace/name`.
    pub library: String,
    /// That library's kind, `movies` or `series`.
    pub kind: String,
    /// The title's provider-scoped id inside its library.
    pub id: String,
    /// The name a person reads.
    pub title: String,
    /// The year or the date of release, as the catalog stores it.
    pub released: String,
    /// The path of the primary art, relative to the library root.
    pub art: String,
    /// What the person did on this title, joined with `, `:
    /// `Director`, `Writer`, then `as <role>` for an actor.
    pub parts: String,
}

/// What the views read. Every list comes back in the order the views draw
/// it, so the views sort nothing: titles by the scanner's sort key, and
/// episodes by their aired numbers.
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

    /// One movie's details, or nothing where the library holds no movie
    /// under that id.
    fn movie(&mut self, library: &str, id: &str) -> Option<MovieDetails>;

    /// One series' details, or nothing where the library holds no series
    /// under that id.
    fn series(&mut self, library: &str, id: &str) -> Option<SeriesDetails>;

    /// Every episode of one series, in aired order: by season, and by
    /// episode inside a season.
    fn episodes(&mut self, library: &str, series: &str) -> Vec<Episode>;

    /// One set and its members in release order, or nothing where the
    /// library holds no set under that id.
    fn set(&mut self, library: &str, id: &str) -> Option<MovieSet>;

    /// The play list one choice resolves to. A movie is one item or
    /// none. An episode is itself and every later episode of its season,
    /// in episode order. A choice whose own main file is missing
    /// resolves to nothing, because a play that skipped what the person
    /// chose is worse than no play at all.
    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem>;

    /// One title's credited people, split by part and in billing
    /// order within a part.
    fn credits(&mut self, library: &str, id: &str) -> Credits;

    /// Every file of one item, in path order, as the foot of a page reads
    /// them.
    fn files(&mut self, library: &str, item: &str) -> Vec<FileFacts>;

    /// One person by the library and the directory that name them,
    /// or nothing where that library holds no such entry.
    fn person(&mut self, library: &str, path: &str) -> Option<Person>;

    /// Every title the person is credited in, across every library
    /// that holds them under a shared id, newest release first and a title
    /// with no release last.
    fn works(&mut self, library: &str, path: &str) -> Vec<Work>;

    /// Whether anything changed since the last call.
    fn changed(&mut self) -> bool;

    /// Take the handle that wakes the loop, for a source with a stream of
    /// its own. A source with no stream takes it and does nothing.
    fn wake_by(&mut self, wake: Waker);
}

/// What a person chose, as the three things that resolve to a play
/// list: a movie by its id, a movie's trailer by the movie's id, and an
/// episode by its series, season, and aired number. The library is not in here because every read
/// takes it beside the choice.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Selection {
    /// One movie, named by its provider-scoped id.
    Movie {
        /// The movie's id inside its library.
        id: String,
    },
    /// One movie's trailer, named by the movie's own id.
    Trailer {
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
            Self::Trailer { id } => format!("{id} trailer"),
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
    /// The catalog's slug for this item, such as `some-film-1999`. The
    /// operator folds the chosen item's slug into the `Play`'s name, so
    /// `kubectl get plays` reads as titles.
    pub slug: String,
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_library_names_itself_after_its_namespace() {
        assert_eq!(library_name("screening/features"), "features");
        assert_eq!(library_name("features"), "features");
    }

    #[test]
    fn every_choice_names_itself_for_the_log() {
        assert_eq!(
            Selection::Movie {
                id: "movie:tmdb:603".into()
            }
            .named(),
            "movie:tmdb:603"
        );
        assert_eq!(
            Selection::Trailer {
                id: "movie:tmdb:603".into()
            }
            .named(),
            "movie:tmdb:603 trailer"
        );
        assert_eq!(
            Selection::Episode {
                series: "series:tvdb:1".into(),
                season: 2,
                episode: 4,
            }
            .named(),
            "series:tvdb:1 S2E4"
        );
    }
}
