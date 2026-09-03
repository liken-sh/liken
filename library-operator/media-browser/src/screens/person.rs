// One person's page. It reads down: the headshot at the left, and
// beside it the name, the dates, and the biography; under both, a wall of
// every title the person is credited in. Focus is always on a slot of
// that wall, so a select opens a title's page, which carries stripes of
// its own.

mod page;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Length, Theme};

use super::{Screen, Step, facts, movie, series};
use crate::catalog::{Source, Work};
use crate::focus;
use crate::posters::Posters;
use crate::views::{Card, wall};

// The file every contributor entry holds their headshot in, and
// the file it holds their biography in.
const HEADSHOT: &str = "headshot.jpg";
const BIOGRAPHY: &str = "biography.txt";

// How much of a biography file the page reads. The page draws a
// few lines of it, so a file longer than this is cut before it is held.
const BIOGRAPHY_CHARS: usize = 2_000;

/// One title the person is credited in, as one slot of the wall.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Credited {
    /// The library that holds the title, as `namespace/name`.
    pub library: String,
    /// That library's kind, `movies` or `series`.
    pub kind: String,
    /// The title's provider-scoped id inside its library.
    pub id: String,
    /// The name a person reads.
    pub name: String,
    /// The line under the slot: the title and its year.
    pub caption: String,
    /// The line under the focused slot: the title and its year, whole
    /// where they fit.
    pub line: facts::Line,
    /// The second line under the slot: the parts the person played.
    pub parts: String,
    /// The path of the title's poster, relative to its library.
    pub art: String,
}

impl Credited {
    // One work as a slot. Both lines are built once here, at the
    // read, and not on every frame.
    fn of(work: Work) -> Self {
        let year = facts::year(&work.released);
        Self {
            caption: facts::joined(&[&work.title, year]),
            line: facts::Line::of(&[&work.title, year]),
            parts: work.parts,
            library: work.library,
            kind: work.kind,
            id: work.id,
            name: work.title,
            art: work.art,
        }
    }
}

impl Card for Credited {
    fn art(&self) -> &str {
        &self.art
    }

    fn library(&self) -> &str {
        &self.library
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn caption(&self) -> &str {
        &self.caption
    }

    fn under(&self) -> &str {
        &self.parts
    }

    fn line_fitting(&self, chars: usize) -> &str {
        self.line.fitting(chars)
    }
}

/// The person's page: the entry the page opened from, the files it
/// draws, the wall of works, and where focus is on that wall.
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
    /// The titles the person is credited in, in the order the
    /// catalog answered them.
    pub works: Vec<Credited>,
    /// The focused work's index.
    pub focus: usize,
}

impl Person {
    /// Read one person's page, or nothing where the library holds
    /// no entry under that directory. Focus lands on the first work.
    pub fn open(library: &str, path: &str, source: &mut dyn Source) -> Option<Self> {
        let entry = source.person(library, path)?;
        let works = source
            .works(library, path)
            .into_iter()
            .map(Credited::of)
            .collect();
        Some(Self {
            library: library.to_string(),
            path: path.to_string(),
            name: entry.name,
            dates: dates(&entry.born, &entry.died),
            headshot: beside(&entry.headshot_library, &entry.headshot_path, HEADSHOT),
            headshot_library: entry.headshot_library,
            biography_path: beside(&entry.biography_library, &entry.biography_path, BIOGRAPHY),
            biography_library: entry.biography_library,
            biography: String::new(),
            works,
            focus: 0,
        })
    }

    /// Read the page again, because the scanner can write the
    /// entry or its credits while the page is open. Focus stays where it
    /// was, inside what the read answered.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(fresh) = Self::open(&self.library, &self.path, source) else {
            return;
        };
        let focus = self.focus;
        *self = fresh;
        self.focus = focus.min(self.works.len().saturating_sub(1));
    }

    /// Read the biography off the library's volume. It is a file
    /// beside the entry and not a column of the catalog, so it arrives
    /// through the store that resolves every other path of that volume.
    pub fn read_biography<P: Posters>(&mut self, posters: &P) {
        self.biography = String::new();
        if self.biography_path.is_empty() {
            return;
        }
        let Some(file) = posters.file(&self.biography_library, &self.biography_path) else {
            return;
        };
        let Ok(text) = std::fs::read_to_string(file) else {
            return;
        };
        self.biography = text.chars().take(BIOGRAPHY_CHARS).collect();
    }

    /// Fold one press in. The arrows move across the wall, and
    /// select opens the title's own page.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::wall(self.focus, self.works.len(), wall::COLUMNS, key);
            return Step::Stay;
        }
        let Some(work) = self.works.get(self.focus) else {
            return Step::Stay;
        };
        match work.kind.as_str() {
            "series" => match series::Series::open(&work.library, &work.id, source) {
                Some(page) => Step::Open(Screen::Series(Box::new(page))),
                None => Step::Stay,
            },
            _ => match movie::Movie::open(&work.library, &work.id, source) {
                Some(page) => Step::Open(Screen::Movie(Box::new(page))),
                None => Step::Stay,
            },
        }
    }

    /// The library and the backdrop the focused work's page draws
    /// over, so the store decodes it while focus rests.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        let work = self.works.get(self.focus)?;
        let backdrop = match work.kind.as_str() {
            "series" => source.series(&work.library, &work.id)?.backdrop,
            _ => source.movie(&work.library, &work.id)?.backdrop,
        };
        if backdrop.is_empty() {
            return None;
        }
        Some((work.library.clone(), backdrop))
    }

    /// The view: the head and the wall of works, on one canvas.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        iced_widget::canvas(page::Page {
            person: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }
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
