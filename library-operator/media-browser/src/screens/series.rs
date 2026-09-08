// A series' page. One screen holds the whole series: a header over the
// backdrop, and under it a wall of episode stills in aired order, with a
// divider before each season's first row, a strip for each franchise the
// series belongs to after the last season, and the stripes of credited
// people after those. Focus is on a still or on a headshot, and the
// header stays at the top of the frame, because it shows the focused
// episode's facts and plot.

mod layout;
mod page;
pub mod progress;
mod seasons;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Rectangle, Theme};

use super::franchise::strips::{self, Move, Place, Strips};
use super::movie::franchise_press;
use super::{InFranchise, Screen, Step, facts, foot, person, stripes, upnext};
use crate::art::Art;
use crate::catalog::draw::Date;
use crate::catalog::{Progress, Selection, SeriesDetails, Source};
use crate::focus::{self, Run};
use crate::views::curtain::{Curtain, Head, Layer};
use crate::views::{Card, layers, rail, ratings};

/// How many stills a row of the episode wall holds. A still is wider
/// than a poster, so the wall holds fewer across.
pub const COLUMNS: usize = 4;

/// Where focus is on the page.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    /// One still of the episode wall.
    Still(usize),
    /// One bar of the seasons rail.
    Rail(usize),
    /// One rung of the franchise strips: which strip, and the heading or
    /// the member in it.
    Franchise(usize, Place),
    /// One headshot of one stripe: the stripe, and the slot in it.
    Stripe(usize, usize),
}

/// One season, as the divider before its first row draws it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Season {
    /// The aired season number.
    pub number: i64,
    /// The heading at the divider's left: the season, and its year where
    /// the first episode of the season holds one.
    pub name: String,
    /// Where the season's episodes sit in the wall's one order.
    pub run: Run,
}

/// One episode, as a still of the wall and as the facts the header shows
/// while that still has focus.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Still {
    /// The episode's id inside its library, which its files are read by.
    pub id: String,
    /// The aired season number. A play request carries it.
    pub season: i64,
    /// The aired episode number. A play request carries it.
    pub episode: i64,
    /// The name a person reads. A still with no art shows it.
    pub name: String,
    /// The card's first line under the still, the episode's own name,
    /// cut by the shaper at the read to one cell of the wall.
    pub fitted: String,
    /// The card's second line: the episode number in the page's own
    /// spelling, then the runtime, cut to the same cell.
    pub under: String,
    /// The header's line while this still has focus: the season and
    /// episode numbers and the name. The header cuts it to one line.
    pub facts: String,
    /// The header's second line while this still has focus: the runtime
    /// and the air date. It is a line of its own so a long name never
    /// pushes the date out of the header.
    pub aired: String,
    /// The episode's plot. The header draws it in place of the series'
    /// plot while this still has focus.
    pub plot: String,
    /// The path the still draws: the episode's own still, or the art of
    /// its series where the catalog holds no still for the episode.
    /// Empty where the series holds no art either.
    pub art: String,
    /// How far the audience reached in this episode, or nothing where no
    /// play of theirs names it.
    pub progress: Option<Progress>,
}

impl Card for Still {
    fn art(&self) -> &str {
        &self.art
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn fitted(&self) -> &str {
        &self.fitted
    }

    fn under(&self) -> &str {
        &self.under
    }

    fn watched(&self) -> Option<f32> {
        progress::share(self.progress.as_ref())
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
    /// The scores the ratings line draws, in the order it draws them. They
    /// are the series' own, so the line stays while a still holds focus.
    pub ratings: Vec<ratings::Score>,
    /// The tagline, empty where the sidecar named none.
    pub tagline: String,
    /// The series' plot. The header draws it while no still holds focus:
    /// on a series with no episodes, and while a stripe holds focus.
    pub plot: String,
    /// The franchises the series belongs to, one strip each, between the
    /// last season and the stripes.
    pub franchises: Strips,
    /// The credited people, as the stripes after the last season.
    pub stripes: stripes::Stripes,
    /// The studios the series' body names. The foot draws them whatever
    /// episode holds focus.
    pub studios: Vec<String>,
    /// The studios and the focused episode's files, as the block after the
    /// last stripe.
    pub foot: foot::Foot,
    /// The seasons, in aired order, one divider each.
    pub seasons: Vec<Season>,
    /// Every episode of the series, in aired order.
    pub stills: Vec<Still>,
    /// The bars of the rail beside the wall, one per season or per range
    /// of seasons, and none on a series of four seasons or fewer.
    pub bars: Vec<rail::Bar>,
    /// The still the wall last held, which a left press on the rail
    /// returns to.
    entered: usize,
    /// Whether the way into the page named the episode focus is on, so the
    /// progress read leaves focus where that way put it.
    placed: bool,
    // The franchise page this page was opened from, which is the container
    // a play from here follows. It is nothing where the page was reached
    // any other way.
    pub via: Option<InFranchise>,
    /// Where focus is.
    pub focus: Focus,
}

impl Series {
    /// Read one series' page, or nothing where the library holds no
    /// series under that id. Focus lands on the first episode, so the
    /// header shows that episode's plot from the start, and a film is
    /// two presses from the wall.
    pub fn open(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        let mut page = Self::read(library, id, source)?;
        page.refoot(source);
        Some(page)
    }

    /// The page with focus on the episode these aired numbers name, which
    /// is where a select on an episode still lands. Focus stays on the first
    /// episode where the series holds no such episode.
    pub fn open_at(
        library: &str,
        id: &str,
        numbers: (i64, i64),
        source: &mut dyn Source,
    ) -> Option<Self> {
        let mut page = Self::read(library, id, source)?;
        let (season, episode) = numbers;
        if let Some(index) = page
            .stills
            .iter()
            .position(|still| still.season == season && still.episode == episode)
        {
            page.focus = Focus::Still(index);
            page.placed = true;
        }
        page.refoot(source);
        Some(page)
    }

    // The page before its foot is read, with focus on the first episode.
    fn read(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        let details = source.series(library, id)?;
        let (stills, seasons) =
            seasons::wall_of(source.episodes(library, id), &Date::today().iso());
        let bars = seasons::bars(&seasons, layout::rail_region());
        Some(Self {
            library: library.to_string(),
            id: id.to_string(),
            title: details.title.clone(),
            logo: details.logo.clone(),
            backdrop: details.backdrop.clone(),
            facts: facts_of(&details),
            ratings: ratings::scores(&details.ratings),
            tagline: details.tagline.clone(),
            plot: details.plot.clone(),
            franchises: Strips::of(library, id, source),
            stripes: stripes::Stripes::of(source.credits(library, id)),
            studios: details.studios.clone(),
            foot: foot::Foot::default(),
            seasons,
            stills,
            bars,
            entered: 0,
            placed: false,
            via: None,
            focus: Focus::Still(0),
        })
    }

    // The foot follows the focused episode, so its lines change with focus
    // as the header's do. Focus on a stripe keeps the lines of the episode
    // the wall last held.
    fn refoot(&mut self, source: &mut dyn Source) {
        let Some(id) = self.focused().map(|still| still.id.clone()) else {
            return;
        };
        self.foot = foot::Foot::of(&self.studios, &source.files(&self.library, &id));
    }

    /// Read the page again, because the scanner can write the series or
    /// its episodes while the page is open. Focus stays where it was,
    /// inside what the read answered, and not on the episode a first
    /// read of the progress would land it on.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(mut fresh) = Self::open(&self.library, &self.id, source) else {
            return;
        };
        fresh.placed = true;
        fresh.via = self.via.clone();
        let focus = self.focus;
        *self = fresh;
        self.focus = self.hold(focus);
    }

    // Where focus lands after a re-read: where it was, unless the
    // wall or the stripe it was on grew shorter.
    fn hold(&self, focus: Focus) -> Focus {
        match focus {
            Focus::Still(index) => Focus::Still(index.min(self.stills.len().saturating_sub(1))),
            Focus::Rail(..) if self.bars.is_empty() => Focus::Still(0),
            Focus::Rail(bar) => Focus::Rail(bar.min(self.bars.len() - 1)),
            Focus::Franchise(strip, place) => match self.franchises.held((strip, place)) {
                Some((strip, place)) => Focus::Franchise(strip, place),
                None => Focus::Still(0),
            },
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
            Focus::Rail(..) | Focus::Franchise(..) | Focus::Stripe(..) => None,
        }
    }

    /// Fold one press in. Left and right move inside one season, up and
    /// down move by a row and cross the dividers, down from the last row
    /// reaches the franchise strips and then the stripes, and select plays
    /// the episode and the rest of its season.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        let held = self.focus;
        let step = match self.focus {
            Focus::Still(index) => self.on_still(index, key, source),
            Focus::Rail(bar) => self.on_rail(bar, key, source),
            Focus::Franchise(strip, place) => self.on_franchise((strip, place), key, source),
            Focus::Stripe(stripe, slot) => self.on_stripe((stripe, slot), key, source),
        };
        // An up that moved nothing is the browser's: the strip over every
        // screen takes focus on it.
        match key == "up" && self.focus == held && matches!(step, Step::Stay) {
            true => Step::Still,
            false => step,
        }
    }

    fn on_still(&mut self, index: usize, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.entered = index;
            self.focus = match key {
                "right" => match seasons::onto(self, index) {
                    Some(bar) => Focus::Rail(bar),
                    None => self.moved(index, key),
                },
                _ => self.moved(index, key),
            };
            self.refoot(source);
            return Step::Stay;
        }
        let Some(still) = self.stills.get(index) else {
            return Step::Stay;
        };
        let numbers = (still.season, still.episode);
        let start = progress::start(still);
        Step::Play {
            library: self.library.clone(),
            selection: Selection::Episode {
                series: self.id.clone(),
                season: numbers.0,
                episode: numbers.1,
            },
            start,
            next: upnext::after_episode(
                source,
                &self.library,
                &self.id,
                &self.title,
                numbers,
                self.via.as_ref(),
            ),
        }
    }

    // Where one press inside the wall lands: a still, or the rung under
    // the wall where down leaves the last row.
    fn moved(&self, index: usize, key: &str) -> Focus {
        let runs: Vec<Run> = self.seasons.iter().map(|season| season.run).collect();
        let moved = focus::sectioned(index, &runs, COLUMNS, key);
        match (key, moved == index) {
            ("down", true) => self.under_wall(moved),
            _ => Focus::Still(moved),
        }
    }

    // One press while a bar of the rail holds focus. The foot is read
    // again because a left or a select puts focus back on a still.
    fn on_rail(&mut self, bar: usize, key: &str, source: &mut dyn Source) -> Step {
        self.focus = seasons::key(self, bar, key);
        self.refoot(source);
        Step::Stay
    }

    // The rung under the last row of stills: the first franchise strip,
    // then the first stripe, and the still itself where the page holds
    // neither.
    fn under_wall(&self, index: usize) -> Focus {
        if let Some((strip, place)) = self.franchises.first() {
            return Focus::Franchise(strip, place);
        }
        match self.stripes.first() {
            Some((stripe, slot)) => Focus::Stripe(stripe, slot),
            None => Focus::Still(index),
        }
    }

    // The rung over the first stripe: the last franchise strip, and the
    // wall's last still where the page holds none.
    fn over_stripes(&self, rung: stripes::Rung) -> Focus {
        if let Some((strip, place)) = self.franchises.last() {
            return Focus::Franchise(strip, place);
        }
        match self.stills.len() {
            0 => Focus::Stripe(rung.0, rung.1),
            count => Focus::Still(count - 1),
        }
    }

    // One press on a franchise strip. A select on the heading opens the
    // franchise's page, and a select on a member opens that member's, the
    // way it does from a film's page.
    fn on_franchise(&mut self, rung: strips::Rung, key: &str, source: &mut dyn Source) -> Step {
        if key == "enter" {
            return franchise_press(&self.franchises, rung, source);
        }
        self.focus = match self.franchises.key(rung, key) {
            Move::To((strip, place)) => Focus::Franchise(strip, place),
            Move::Above => match self.stills.len() {
                0 => Focus::Franchise(rung.0, rung.1),
                count => Focus::Still(count - 1),
            },
            Move::Below => match self.stripes.first() {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Franchise(rung.0, rung.1),
            },
        };
        self.refoot(source);
        Step::Stay
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
            // Up from the first stripe returns to the last franchise
            // strip, then to the wall's last row, and stays where the
            // series holds neither.
            None => self.over_stripes(rung),
        };
        self.refoot(source);
        Step::Stay
    }

    /// The view: the backdrop behind the header, the scrim over it, the
    /// header and the wall over both, and the loading state's curtain
    /// over the page while that state runs.
    pub fn view<'a, A: Art>(
        &'a self,
        store: &'a RefCell<A>,
        curtain: Option<Curtain>,
        held: bool,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        layers::Page {
            library: &self.library,
            art: &self.backdrop,
            store,
            ground: layers::Ground::Below(layout::head()),
            front: page::Page {
                series: self,
                store,
                lifted: curtain.is_some(),
                held,
            },
            over: curtain.map(|curtain| Layer {
                library: &self.library,
                art: &self.backdrop,
                logo: &self.logo,
                name: &self.title,
                store,
                head: self,
                curtain,
            }),
        }
        .view()
    }
}

impl Head for Series {
    fn head(&self, bounds: Rectangle) -> Rectangle {
        page::head(bounds)
    }
}

/// The facts line of one series: the year, the season count, the
/// content rating, and the genres, the way a film's line reads.
pub(crate) fn facts_of(details: &SeriesDetails) -> String {
    facts::joined(&[&facts_without_genres(details), &details.genres.join(", ")])
}

/// The same line without the genres, for the home page's banner, which
/// draws the genres on a line of its own.
pub(crate) fn facts_without_genres(details: &SeriesDetails) -> String {
    facts::joined(&[
        facts::year(&details.released),
        &seasons_of(details.seasons),
        &details.rating,
    ])
}

// The season count as a person reads it, and nothing at all for a series
// whose episodes have not landed yet.
pub(crate) fn seasons_of(seasons: i64) -> String {
    match seasons {
        0 => String::new(),
        1 => "1 season".to_string(),
        count => format!("{count} seasons"),
    }
}

#[cfg(test)]
mod tests;
