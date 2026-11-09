// One person's page. It reads down: the headshot at the left, and beside
// it the name, the facts line, and the biography; under them the career
// axis, a timeline of every dated credit; and under the axis one row of
// posters, oldest first. The axis shows when the person worked and what
// the people at the screen have seen of it, and the row shows the works.
// Focus is on the axis or on a poster, and a select on a poster opens
// the title's page. Plan 88 holds the design.

// The axis of a career: its span, its years, and its marks.
mod axis;
// One credit: its poster, its year, and what the audience has seen of it.
mod credit;
// The dates of the life, and the ages they give each credit.
mod life;
// The motion of the page: where the outline goes, and the glides of the
// row, the bright part of the line, and the label.
mod motion;
mod page;
// The row of posters: its two rests and where each poster goes.
mod row;
// What the people at the screen have seen of each credit.
mod seen;
// The facts line under the name.
mod summary;
// Where focus is, and what each press does to it.
mod walk;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Length, Theme};

use self::axis::Axis;
use self::credit::Credit;
use self::life::Life;
use self::walk::{Pressed, Walk};
use super::slots;
use super::{Step, facts};
use crate::art::Art;
use crate::audience::Viewer;
use crate::audience::faces::Faces;
use crate::catalog::draw::Date;
use crate::catalog::{self, Query, Source};
use crate::motion::Glide;

pub use self::walk::Focus;

// The file every contributor entry holds their headshot in, and
// the file it holds their biography in.
const HEADSHOT: &str = "headshot.jpg";
const BIOGRAPHY: &str = "biography.txt";

// How much of a biography file the page reads. The page draws a
// few lines of it, so a file longer than this is cut before it is held.
const BIOGRAPHY_CHARS: usize = 2_000;

/// The person's page: the entry the page opened from, the files it
/// draws, the credits, the axis over them, and where focus is.
#[derive(Debug)]
pub struct Person {
    /// The library the page opened from, as `namespace/name`.
    pub library: String,
    /// The person's directory in that library.
    pub path: String,
    /// The name a person reads.
    pub name: String,
    /// The born and died dates on one line, empty where the entry
    /// holds neither.
    pub dates: String,
    /// The line under the name: the dates, the credits and their years,
    /// and how many of them everyone at the screen has seen.
    pub facts: String,
    /// The library the headshot resolves against, empty where no
    /// library holds one.
    pub headshot_library: String,
    /// The path of the headshot file, empty where no library holds
    /// one.
    pub headshot: String,
    /// The library the biography file resolves against, empty where
    /// no library holds one.
    pub biography_library: String,
    /// The path of the biography file, empty where no library holds
    /// one.
    pub biography_path: String,
    /// The biography, once the page read it off the volume. The
    /// page cuts it to a few lines.
    pub biography: String,
    /// Every credit, oldest first, with the undated ones last.
    pub credits: Vec<Credit>,
    /// The career axis, and nothing for a career too short to draw one.
    pub axis: Option<Axis>,
    /// Where focus is, and where the row of posters rests.
    pub walk: Walk,
    life: Life,
    // The current year, which "now" on the axis and in the facts line is.
    now: i64,
    // How many people are at the screen, which the facts line counts for.
    people: usize,
    // How far the row has scrolled, the two ends of the bright part of
    // the line, and the center of the label, as each eases to where a
    // press put it.
    row: Glide,
    window: (Glide, Glide),
    label: Glide,
}

impl Person {
    /// Read one person's page, or nothing where the library holds no
    /// entry under that directory. Focus lands on the newest dated
    /// credit.
    pub fn open(library: &str, path: &str, source: &mut dyn Source) -> Option<Self> {
        Self::open_from(library, path, None, source)
    }

    /// Read one person's page from the page of a title, by its library
    /// and its id, so focus lands on that title's credit and the row shows
    /// where it is in the career.
    pub fn open_from(
        library: &str,
        path: &str,
        via: Option<&(String, String)>,
        source: &mut dyn Source,
    ) -> Option<Self> {
        let mut page = Self::read(library, path, Date::today().year, source)?;
        page.walk = Walk::opened(&page.credits, via);
        Some(page)
    }

    // The page as the catalog holds it at the year `now`, with focus where
    // a page opens on.
    fn read(library: &str, path: &str, now: i64, source: &mut dyn Source) -> Option<Self> {
        let entry = source.person(library, path)?;
        let query = Query::Person {
            library: library.to_string(),
            path: path.to_string(),
        };
        let slots = credit::ordered(source.wall(&query).slots);
        let life = Life::of(
            &entry.born,
            &entry.died,
            slots.iter().map(|slot| slot.released.as_str()),
        );
        let mut credits: Vec<Credit> = slots
            .into_iter()
            .map(|slot| Credit::of(&query, slot, &life, now, source))
            .collect();
        let axis = Axis::of(&credits, &life, now);
        // The row's cells are narrower on a page with an axis, so the
        // cards are cut once the read knows whether the page draws one.
        let band = page::band(axis.is_some());
        for credit in &mut credits {
            credit.item.fit(band);
        }
        let dates = dates(&entry.born, &entry.died);
        Some(Self {
            library: library.to_string(),
            path: path.to_string(),
            name: entry.name,
            facts: summary::line(&dates, &credits, now, 0),
            dates,
            headshot: beside(&entry.headshot_library, &entry.headshot_path, HEADSHOT),
            headshot_library: entry.headshot_library,
            biography_path: beside(&entry.biography_library, &entry.biography_path, BIOGRAPHY),
            biography_library: entry.biography_library,
            biography: String::new(),
            axis,
            walk: Walk::opened(&credits, None),
            credits,
            life,
            now,
            people: 0,
            row: Glide::still(0.0),
            window: (Glide::still(0.0), Glide::still(0.0)),
            label: Glide::still(0.0),
        })
    }

    /// Read the page again, because the scanner can write the entry or
    /// its credits while the page is open. Focus stays on the work and
    /// the year it was on, and at the same place where that work is gone.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(mut fresh) = Self::read(&self.library, &self.path, self.now, source) else {
            return;
        };
        let held = self.walk.held(&self.credits, self.axis.as_ref());
        fresh.walk = self.walk;
        fresh.walk.hold(&held, &fresh.credits, fresh.axis.as_ref());
        fresh.biography = std::mem::take(&mut self.biography);
        fresh.people = self.people;
        fresh.row = self.row;
        fresh.window = self.window;
        fresh.label = self.label;
        *self = fresh;
    }

    /// Read what these people have seen of every credit, and redraw the
    /// marks of the axis and the facts line at once. The browser calls it
    /// at every open and every re-read, because only the browser holds
    /// the audience.
    pub fn read_progress(
        &mut self,
        source: &mut dyn Source,
        people: &[String],
        viewers: &[Viewer],
    ) {
        seen::read(&mut self.credits, source, people, viewers);
        self.people = people.len();
        self.axis = Axis::of(&self.credits, &self.life, self.now);
        self.facts = summary::line(&self.dates, &self.credits, self.now, self.people);
    }

    /// Whether the page draws how far someone got in this work.
    pub fn draws_progress_of(&self, library: &str, item: &str) -> bool {
        self.credits
            .iter()
            .any(|credit| credit.item.library == library && credit.item.id == item)
    }

    /// Read the biography off the library's volume. It is a file
    /// beside the entry and not a column of the catalog, so it arrives
    /// through the store that resolves every other path of that volume.
    pub fn read_biography<A: Art>(&mut self, store: &A) {
        self.biography = String::new();
        if self.biography_path.is_empty() {
            return;
        }
        let Some(file) = store.file(&self.biography_library, &self.biography_path) else {
            return;
        };
        let Ok(text) = std::fs::read_to_string(file) else {
            return;
        };
        self.biography = text.chars().take(BIOGRAPHY_CHARS).collect();
    }

    /// Fold one press in. The arrows move along the axis and the row,
    /// and between them, and select on a poster opens the title's own
    /// page. Up from the axis, or from the row of a page with no axis,
    /// moves nothing, which is how a press reaches the browser's strip.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self.walk.press(key, self.axis.as_ref(), self.credits.len()) {
            Pressed::Moved => Step::Stay,
            Pressed::Still => Step::Still,
            Pressed::Open(index) => slots::opened(&self.credits[index].item, source),
        }
    }

    /// The library and the backdrop the focused work's page draws over,
    /// so the store decodes it while focus rests. While the axis holds
    /// focus, that work is the one the row rests on, which down lands on.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        let index = self.walk.poster().unwrap_or(self.walk.rest.anchor());
        slots::backdrop(&self.credits.get(index)?.item, source)
    }

    /// The view: the head, the axis, and the row, on one canvas. `held`
    /// is whether the page holds focus, and `at` is the second the frame
    /// draws, which every glide eases on.
    pub fn view<'a, A: Art>(
        &'a self,
        store: &'a RefCell<A>,
        faces: &'a Faces,
        held: bool,
        at: f64,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        iced_widget::canvas(page::Page {
            person: self,
            store,
            faces,
            held,
            at,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }
}

/// The library and the path of a person's headshot, both empty where no
/// library holds one. A person's strip draws it on the slot about them.
pub fn headshot(entry: &catalog::Person) -> (String, String) {
    (
        entry.headshot_library.clone(),
        beside(&entry.headshot_library, &entry.headshot_path, HEADSHOT),
    )
}

// The path of one file inside a person's entry, and nothing where
// no library holds that file.
fn beside(library: &str, path: &str, file: &str) -> String {
    match library.is_empty() {
        true => String::new(),
        false => format!("{path}/{file}"),
    }
}

/// The born and died years on one line, and nothing where the
/// entry holds neither date.
pub fn dates(born: &str, died: &str) -> String {
    match (facts::year(born), facts::year(died)) {
        ("", "") => String::new(),
        (born, "") => format!("born {born}"),
        ("", died) => format!("died {died}"),
        (born, died) => format!("{born} to {died}"),
    }
}

#[cfg(test)]
mod tests;
