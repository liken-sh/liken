// A series' page. One screen holds the whole series: a header over the
// backdrop, and under it a wall of episode stills in aired order, with a
// divider before each season's first row. Focus is always on a still, and
// the header stays at the top of the frame, because it shows the focused
// episode's facts and plot.

mod layout;
mod page;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Theme};

use super::{Step, facts};
use crate::catalog::{Episode, Selection, SeriesDetails, Source};
use crate::focus::{self, Run};
use crate::posters::Posters;
use crate::views::{Card, layers};

/// How many stills a row of the episode wall holds. A still is wider
/// than a poster, so the wall holds fewer across.
pub const COLUMNS: usize = 4;

/// One season, as the divider before its first row draws it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Season {
    /// The aired season number.
    pub number: i64,
    /// The name at the divider's left.
    pub name: String,
    /// The year at the divider's right: the year of the first episode
    /// that aired in this season.
    pub year: String,
    /// Where the season's episodes sit in the wall's one order.
    pub run: Run,
}

/// One episode, as a still of the wall and as the facts the header shows
/// while that still has focus.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Still {
    /// The aired season number. A play request carries it.
    pub season: i64,
    /// The aired episode number. A play request carries it.
    pub episode: i64,
    /// The name a person reads. A still with no art shows it.
    pub name: String,
    /// The caption under the still: the episode number and the name.
    pub caption: String,
    /// The caption under the still while it holds focus: the caption and
    /// the runtime.
    pub line: facts::Line,
    /// The header's line while this still has focus: the season and
    /// episode numbers, the name, and the runtime.
    pub facts: String,
    /// The episode's plot. The header draws it in place of the series'
    /// plot while this still has focus.
    pub plot: String,
    /// The path of the episode's still, empty where it has none.
    pub art: String,
}

impl Card for Still {
    fn art(&self) -> &str {
        &self.art
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn caption(&self) -> &str {
        &self.caption
    }

    fn line_fitting(&self, chars: usize) -> &str {
        self.line.fitting(chars)
    }
}

/// The series page: the words it draws, the art it draws them over, its
/// episodes in aired order, and where focus is. Every line is built once
/// here, at the read, and not on every frame.
#[derive(Debug)]
pub struct Series {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The series' id inside that library.
    pub id: String,
    /// The name a person reads. The page draws it where the series has no
    /// logo.
    pub title: String,
    /// The path of the logo file, empty where the series has none.
    pub logo: String,
    /// The path of the backdrop file, empty where the series has none.
    pub backdrop: String,
    /// The year, the season count, and the content rating, on one line.
    pub facts: String,
    /// The tagline, empty where the sidecar named none.
    pub tagline: String,
    /// The series' plot. The header draws it only on a series with no
    /// episodes, because a still always holds focus otherwise.
    pub plot: String,
    /// The cast, as names with the parts they played.
    pub cast: String,
    /// The seasons, in aired order, one divider each.
    pub seasons: Vec<Season>,
    /// Every episode of the series, in aired order.
    pub stills: Vec<Still>,
    /// The focused still's index.
    pub focus: usize,
}

impl Series {
    /// Read one series' page, or nothing where the library holds no
    /// series under that id. Focus lands on the first episode, so the
    /// header shows that episode's plot from the start, and a film is
    /// two presses from the wall.
    pub fn open(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        let details = source.series(library, id)?;
        let (stills, seasons) = wall_of(source.episodes(library, id));
        Some(Self {
            library: library.to_string(),
            id: id.to_string(),
            title: details.title.clone(),
            logo: details.logo.clone(),
            backdrop: details.backdrop.clone(),
            facts: facts_of(&details),
            tagline: details.tagline.clone(),
            plot: details.plot.clone(),
            cast: facts::cast(&details.cast),
            seasons,
            stills,
            focus: 0,
        })
    }

    /// Read the page again, because the scanner can write the series or
    /// its episodes while the page is open. Focus stays where it was,
    /// inside what the read answered.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(fresh) = Self::open(&self.library, &self.id, source) else {
            return;
        };
        let focus = self.focus;
        *self = fresh;
        self.focus = focus.min(self.stills.len().saturating_sub(1));
    }

    /// Fold one press in. Left and right move inside one season, up and
    /// down move by a row and cross the dividers, and select plays the
    /// episode and the rest of its season.
    pub fn key(&mut self, key: &str, _source: &mut dyn Source) -> Step {
        if key != "enter" {
            let runs: Vec<Run> = self.seasons.iter().map(|season| season.run).collect();
            self.focus = focus::sectioned(self.focus, &runs, COLUMNS, key);
            return Step::Stay;
        }
        let Some(still) = self.stills.get(self.focus) else {
            return Step::Stay;
        };
        Step::Play {
            library: self.library.clone(),
            selection: Selection::Episode {
                series: self.id.clone(),
                season: still.season,
                episode: still.episode,
            },
        }
    }

    /// The view: the backdrop behind the header, the scrim over it, and
    /// the header and the wall over both.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        layers::Page {
            library: &self.library,
            art: &self.backdrop,
            posters,
            ground: layers::Ground::Below(layout::head()),
            front: page::Page {
                series: self,
                posters,
            },
        }
        .view()
    }
}

// The wall and its dividers out of one read of the episodes. The rows
// arrive in aired order, so a season starts wherever the season number
// changes, and its year is the year of the first episode that aired in
// it.
fn wall_of(episodes: Vec<Episode>) -> (Vec<Still>, Vec<Season>) {
    let mut stills = Vec::with_capacity(episodes.len());
    let mut seasons: Vec<Season> = Vec::new();
    for (index, episode) in episodes.into_iter().enumerate() {
        match seasons.last_mut() {
            Some(season) if season.number == episode.season => season.run.count += 1,
            _ => seasons.push(Season {
                number: episode.season,
                name: format!("Season {}", episode.season),
                year: facts::year(&episode.released).to_string(),
                run: Run {
                    first: index,
                    count: 1,
                },
            }),
        }
        stills.push(still_of(episode));
    }
    (stills, seasons)
}

fn still_of(episode: Episode) -> Still {
    let numbers = format!("S{} E{}", episode.season, episode.episode);
    let runtime = facts::runtime(episode.duration);
    let caption = facts::joined(&[&format!("E{}", episode.episode), &episode.title]);
    Still {
        line: facts::Line::of(&[&caption, &runtime]),
        caption,
        facts: facts::joined(&[&numbers, &episode.title, &runtime]),
        season: episode.season,
        episode: episode.episode,
        name: episode.title,
        plot: episode.plot,
        art: episode.art,
    }
}

fn facts_of(details: &SeriesDetails) -> String {
    facts::joined(&[
        facts::year(&details.released),
        &seasons_of(details.seasons),
        &details.rating,
    ])
}

// The season count as a person reads it, and nothing at all for a series
// whose episodes have not landed yet.
fn seasons_of(seasons: i64) -> String {
    match seasons {
        0 => String::new(),
        1 => "1 season".to_string(),
        count => format!("{count} seasons"),
    }
}

#[cfg(test)]
mod tests;
