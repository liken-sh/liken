// A series' page. One screen holds the whole series: a header over the
// backdrop, and under it a wall of episode stills in aired order, with a
// divider before each season's first row, and the stripes of credited
// people after the last season. Focus is on a still or on a headshot, and
// the header stays at the top of the frame, because it shows the focused
// episode's facts and plot.

mod layout;
mod page;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Theme};

use super::{Screen, Step, facts, person, stripes};
use crate::catalog::{Episode, Selection, SeriesDetails, Source};
use crate::focus::{self, Run};
use crate::posters::Posters;
use crate::views::{Card, layers};

/// How many stills a row of the episode wall holds. A still is wider
/// than a poster, so the wall holds fewer across.
pub const COLUMNS: usize = 4;

/// Where focus is on the page.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    /// One still of the episode wall.
    Still(usize),
    /// One headshot of one stripe: the stripe, and the slot in it.
    Stripe(usize, usize),
}

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
    /// The series' plot. The header draws it while no still holds focus:
    /// on a series with no episodes, and while a stripe holds focus.
    pub plot: String,
    /// The credited people, as the stripes after the last season.
    pub stripes: stripes::Stripes,
    /// The seasons, in aired order, one divider each.
    pub seasons: Vec<Season>,
    /// Every episode of the series, in aired order.
    pub stills: Vec<Still>,
    /// Where focus is.
    pub focus: Focus,
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
            stripes: stripes::Stripes::of(source.credits(library, id)),
            seasons,
            stills,
            focus: Focus::Still(0),
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
        self.focus = self.hold(focus);
    }

    // Where focus lands after a re-read: where it was, unless the
    // wall or the stripe it was on grew shorter.
    fn hold(&self, focus: Focus) -> Focus {
        match focus {
            Focus::Still(index) => Focus::Still(index.min(self.stills.len().saturating_sub(1))),
            Focus::Stripe(stripe, slot) => match self.stripes.held((stripe, slot)) {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Still(0),
            },
        }
    }

    /// The still the header draws the facts and the plot of, or
    /// nothing while a stripe holds focus and on a series whose episodes
    /// have not landed.
    pub fn focused(&self) -> Option<&Still> {
        match self.focus {
            Focus::Still(index) => self.stills.get(index),
            Focus::Stripe(..) => None,
        }
    }

    /// Fold one press in. Left and right move inside one season, up and
    /// down move by a row and cross the dividers, down from the last row
    /// reaches the stripes, and select plays the episode and the rest of
    /// its season.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self.focus {
            Focus::Still(index) => self.on_still(index, key),
            Focus::Stripe(stripe, slot) => self.on_stripe((stripe, slot), key, source),
        }
    }

    fn on_still(&mut self, index: usize, key: &str) -> Step {
        if key != "enter" {
            let runs: Vec<Run> = self.seasons.iter().map(|season| season.run).collect();
            let moved = focus::sectioned(index, &runs, COLUMNS, key);
            self.focus = match (key, moved == index, self.stripes.first()) {
                ("down", true, Some((stripe, slot))) => Focus::Stripe(stripe, slot),
                _ => Focus::Still(moved),
            };
            return Step::Stay;
        }
        let Some(still) = self.stills.get(index) else {
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

    // One press on a stripe. Select opens the person's page, and a
    // name the credits could not resolve opens nothing.
    fn on_stripe(&mut self, rung: stripes::Rung, key: &str, source: &mut dyn Source) -> Step {
        if key == "enter" {
            let Some(face) = self.stripes.face(rung) else {
                return Step::Stay;
            };
            if face.contributor.is_empty() {
                return Step::Stay;
            }
            return match person::Person::open(&self.library, &face.contributor, source) {
                Some(page) => Step::Open(Screen::Person(Box::new(page))),
                None => Step::Stay,
            };
        }
        self.focus = match self.stripes.key(rung, key) {
            Some((stripe, slot)) => Focus::Stripe(stripe, slot),
            // Up from the first stripe returns to the wall's last
            // row, and stays where the series holds no episode.
            None => match self.stills.len() {
                0 => Focus::Stripe(rung.0, rung.1),
                count => Focus::Still(count - 1),
            },
        };
        Step::Stay
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
