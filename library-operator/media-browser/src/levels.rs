// This module turns a library's kind into a stack of levels as data,
// so the views draw walls and lists and hold no code per kind.

use crate::catalog::{EpisodeRow, LibraryEntry, Source, Title};

/// The two shapes a level draws as: a wall of posters, or a vertical
/// list. The drawing code draws these two and nothing else.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Shape {
    /// A grid of poster slots with the focused one larger.
    Wall,
    /// A vertical list with an art thumbnail beside each row.
    List,
}

// The table that gives each kind its levels; a new kind is a new row
// here and no new drawing code, and depth in the table is depth in the
// catalog: titles, then seasons, then episodes.
const KINDS: &[(&str, &[Shape])] = &[
    ("movies", &[Shape::Wall]),
    ("series", &[Shape::List, Shape::List, Shape::List]),
];

/// The shapes a kind's levels draw as, top level first; a kind the
/// table does not name gets one wall of its top list.
pub fn shapes_of(kind: &str) -> &'static [Shape] {
    KINDS
        .iter()
        .find(|(name, _)| *name == kind)
        .map_or(&[Shape::Wall], |(_, shapes)| shapes)
}

/// One drawable row, the same struct for a library, a title, a season,
/// and an episode, so the views draw one thing.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Row {
    /// What a descent passes down: the library key, the item id, or
    /// the season number as text.
    pub id: String,
    /// The name a person reads.
    pub name: String,
    /// The secondary line: a release date, a kind and count, or the
    /// season and episode numbers.
    pub detail: String,
    /// The art path the poster store resolves, empty where the row
    /// has none.
    pub art: String,
    /// The library's kind, carried only on a library row because the
    /// descent into it needs the kind's level table.
    pub kind: String,
    /// The aired episode number, carried only on an episode row because
    /// a play request names the episode it starts from by that number.
    /// Every other row carries zero.
    pub number: i64,
}

impl Row {
    fn library(entry: LibraryEntry) -> Self {
        // The first screen shows the name half of namespace/name.
        let name = entry
            .library
            .split_once('/')
            .map_or(entry.library.as_str(), |(_, name)| name)
            .to_string();
        Self {
            id: entry.library,
            name,
            detail: format!("{} · {}", entry.kind, entry.items),
            art: String::new(),
            kind: entry.kind,
            number: 0,
        }
    }

    fn title(title: Title) -> Self {
        Self {
            id: title.id,
            name: title.title,
            detail: title.released,
            art: title.art,
            kind: String::new(),
            number: 0,
        }
    }

    fn season(season: i64) -> Self {
        Self {
            id: season.to_string(),
            name: format!("Season {season}"),
            detail: String::new(),
            art: String::new(),
            kind: String::new(),
            number: 0,
        }
    }

    fn episode(episode: EpisodeRow) -> Self {
        Self {
            id: episode.id,
            name: episode.title,
            detail: format!("S{} E{}", episode.season, episode.episode),
            art: episode.art,
            kind: String::new(),
            number: episode.episode,
        }
    }
}

/// What one level reads from the source, so a level re-reads exactly
/// itself when the source reports a change.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Fetch {
    /// The first screen's read, every library in the catalog.
    Libraries,
    /// A library's top list, depth zero of any kind.
    Titles {
        /// The library key, namespace/name.
        library: String,
        /// The kind, which names the item table.
        kind: String,
    },
    /// One series' seasons, depth one.
    Seasons {
        /// The library key, namespace/name.
        library: String,
        /// The kind, kept so the next descent finds its shape.
        kind: String,
        /// The series id the seasons belong to.
        series: String,
    },
    /// One season's episodes, depth two.
    Episodes {
        /// The library key, namespace/name.
        library: String,
        /// The series id the episodes belong to.
        series: String,
        /// The aired season number.
        season: i64,
    },
}

impl Fetch {
    /// The library the poster store resolves art against, empty on
    /// the first screen where rows carry no art.
    pub fn library(&self) -> &str {
        match self {
            Self::Libraries => "",
            Self::Titles { library, .. }
            | Self::Seasons { library, .. }
            | Self::Episodes { library, .. } => library,
        }
    }

    fn read<S: Source>(&self, source: &mut S) -> Vec<Row> {
        match self {
            Self::Libraries => source.libraries().into_iter().map(Row::library).collect(),
            Self::Titles { library, kind } => source
                .titles(library, kind)
                .into_iter()
                .map(Row::title)
                .collect(),
            Self::Seasons {
                library, series, ..
            } => source
                .seasons(library, series)
                .into_iter()
                .map(Row::season)
                .collect(),
            Self::Episodes {
                library,
                series,
                season,
            } => source
                .episodes(library, series, *season)
                .into_iter()
                .map(Row::episode)
                .collect(),
        }
    }
}

/// One level of the navigation stack: its shape, its rows, its focus,
/// and the fetch that refreshes it.
#[derive(Debug)]
pub struct Level {
    /// How the level draws, a wall or a list.
    pub shape: Shape,
    /// What re-reads this level.
    pub fetch: Fetch,
    /// The rows in the order the source returned them.
    pub rows: Vec<Row>,
    /// The focused row's index; on a covered level it is also the
    /// selection the level above descended through.
    pub focus: usize,
}

impl Level {
    /// Read a fresh level with focus at its first row.
    pub fn new<S: Source>(shape: Shape, fetch: Fetch, source: &mut S) -> Self {
        let rows = fetch.read(source);
        Self {
            shape,
            fetch,
            rows,
            focus: 0,
        }
    }

    /// Re-read this level's rows and keep focus in range, because a
    /// change can remove the focused row.
    pub fn reread<S: Source>(&mut self, source: &mut S) {
        self.rows = self.fetch.read(source);
        self.focus = self.focus.min(self.rows.len().saturating_sub(1));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn movies_are_one_wall() {
        assert_eq!(shapes_of("movies"), &[Shape::Wall]);
    }

    #[test]
    fn series_are_three_lists() {
        assert_eq!(
            shapes_of("series"),
            &[Shape::List, Shape::List, Shape::List]
        );
    }

    #[test]
    fn an_unknown_kind_is_one_wall() {
        assert_eq!(shapes_of("photos"), &[Shape::Wall]);
    }

    #[test]
    fn a_library_row_shows_the_name_half() {
        let row = Row::library(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 42,
        });
        assert_eq!(row.name, "features");
        assert_eq!(row.id, "screening/features");
        assert_eq!(row.detail, "movies · 42");
        assert_eq!(row.kind, "movies");
    }

    #[test]
    fn a_library_row_without_a_namespace_keeps_its_whole_name() {
        let row = Row::library(LibraryEntry {
            library: "features".into(),
            kind: "movies".into(),
            items: 1,
        });
        assert_eq!(row.name, "features");
    }

    #[test]
    fn a_title_row_carries_its_release() {
        let row = Row::title(Title {
            id: "movie:sample:1".into(),
            title: "Specimen 0001".into(),
            released: "1987".into(),
            art: "posters/1.jpg".into(),
        });
        assert_eq!(row.detail, "1987");
        assert_eq!(row.art, "posters/1.jpg");
    }

    #[test]
    fn a_season_row_names_its_number() {
        let row = Row::season(3);
        assert_eq!(row.name, "Season 3");
        assert_eq!(row.id, "3");
    }

    #[test]
    fn an_episode_row_carries_its_numbers() {
        let row = Row::episode(EpisodeRow {
            id: "episode:sample:1".into(),
            title: "Segment 04".into(),
            season: 2,
            episode: 4,
            art: String::new(),
        });
        assert_eq!(row.detail, "S2 E4");
        assert_eq!(row.number, 4);
    }

    #[test]
    fn every_fetch_names_its_library() {
        assert_eq!(Fetch::Libraries.library(), "");
        let titles = Fetch::Titles {
            library: "a/b".into(),
            kind: "movies".into(),
        };
        assert_eq!(titles.library(), "a/b");
        let seasons = Fetch::Seasons {
            library: "a/b".into(),
            kind: "series".into(),
            series: "s".into(),
        };
        assert_eq!(seasons.library(), "a/b");
        let episodes = Fetch::Episodes {
            library: "a/b".into(),
            series: "s".into(),
            season: 1,
        };
        assert_eq!(episodes.library(), "a/b");
    }
}
