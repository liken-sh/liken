// The two lists a series library descends through: its seasons, and one
// season's episodes. Plan 22 replaces both with one series page. Until
// that page lands, these two keep a series library walkable.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Theme};

use super::{Screen, Step};
use crate::catalog::{EpisodeRow, Selection, Source};
use crate::focus;
use crate::posters::Posters;
use crate::views::Card;
use crate::views::list::List;

/// One row of either list.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Row {
    /// The name a person reads.
    pub name: String,
    /// The second line, at the row's right.
    pub detail: String,
    /// The art path the poster store resolves, empty where the row has
    /// none.
    pub art: String,
    /// The season number or the aired episode number the row stands for.
    /// A descent and a play request carry it.
    pub number: i64,
}

impl Card for Row {
    fn art(&self) -> &str {
        &self.art
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn detail(&self) -> &str {
        &self.detail
    }
}

/// One series' seasons.
#[derive(Debug)]
pub struct Seasons {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The id of the series the seasons belong to.
    pub series: String,
    /// The seasons in aired order.
    pub rows: Vec<Row>,
    /// The focused season's index.
    pub focus: usize,
}

impl Seasons {
    /// Read one series' seasons, with focus on the first of them.
    pub fn open(library: &str, series: &str, source: &mut dyn Source) -> Self {
        Self {
            library: library.to_string(),
            series: series.to_string(),
            rows: seasons(library, series, source),
            focus: 0,
        }
    }

    /// Read the seasons again and keep focus in range.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.rows = seasons(&self.library, &self.series, source);
        self.focus = self.focus.min(self.rows.len().saturating_sub(1));
    }

    /// Fold one press in. Select opens that season's episodes.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::list(self.focus, self.rows.len(), key);
            return Step::Stay;
        }
        let Some(row) = self.rows.get(self.focus) else {
            return Step::Stay;
        };
        Step::Open(Screen::Episodes(Episodes::open(
            &self.library,
            &self.series,
            row.number,
            source,
        )))
    }

    /// The view, a list with no art beside its rows.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        rows(&self.rows, self.focus, &self.library, posters)
    }
}

/// One season's episodes.
#[derive(Debug)]
pub struct Episodes {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The id of the series the episodes belong to.
    pub series: String,
    /// The aired season number.
    pub season: i64,
    /// The episodes in aired order.
    pub rows: Vec<Row>,
    /// The focused episode's index.
    pub focus: usize,
}

impl Episodes {
    /// Read one season's episodes, with focus on the first of them.
    pub fn open(library: &str, series: &str, season: i64, source: &mut dyn Source) -> Self {
        Self {
            library: library.to_string(),
            series: series.to_string(),
            season,
            rows: episodes(library, series, season, source),
            focus: 0,
        }
    }

    /// Read the episodes again and keep focus in range.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.rows = episodes(&self.library, &self.series, self.season, source);
        self.focus = self.focus.min(self.rows.len().saturating_sub(1));
    }

    /// Fold one press in. Select plays the episode and the rest of its
    /// season.
    pub fn key(&mut self, key: &str, _source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::list(self.focus, self.rows.len(), key);
            return Step::Stay;
        }
        let Some(row) = self.rows.get(self.focus) else {
            return Step::Stay;
        };
        Step::Play {
            library: self.library.clone(),
            selection: Selection::Episode {
                series: self.series.clone(),
                season: self.season,
                episode: row.number,
            },
        }
    }

    /// The view, a list with the episode's own art beside each row.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        rows(&self.rows, self.focus, &self.library, posters)
    }
}

fn seasons(library: &str, series: &str, source: &mut dyn Source) -> Vec<Row> {
    source
        .seasons(library, series)
        .into_iter()
        .map(|season| Row {
            name: format!("Season {season}"),
            detail: String::new(),
            art: String::new(),
            number: season,
        })
        .collect()
}

fn episodes(library: &str, series: &str, season: i64, source: &mut dyn Source) -> Vec<Row> {
    source
        .episodes(library, series, season)
        .into_iter()
        .map(episode)
        .collect()
}

fn episode(episode: EpisodeRow) -> Row {
    Row {
        name: episode.title,
        detail: format!("S{} E{}", episode.season, episode.episode),
        art: episode.art,
        number: episode.episode,
    }
}

fn rows<'a, P: Posters>(
    rows: &'a [Row],
    focus: usize,
    library: &'a str,
    posters: &'a RefCell<P>,
) -> Element<'a, Infallible, Theme, Renderer> {
    canvas(List {
        rows,
        focus,
        library,
        posters,
    })
    .width(Length::Fill)
    .height(Length::Fill)
    .into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_episode_row_carries_its_numbers() {
        let row = episode(EpisodeRow {
            id: "episode:sample:1".into(),
            title: "Segment 04".into(),
            season: 2,
            episode: 4,
            art: String::new(),
        });
        assert_eq!(row.name, "Segment 04");
        assert_eq!(row.detail, "S2 E4");
        assert_eq!(row.number, 4);
    }
}
