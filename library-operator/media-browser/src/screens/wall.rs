// One library's titles, as a wall of art under a band. The band carries
// the library's name and its count, and the three controls for sort,
// filter, and search reserve their place at its right. Up from the first
// row of posters reaches the band, and down from the band returns focus
// to the title the wall remembers.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use super::{Item, Screen, Step, movie, series};
use crate::catalog::{Source, library_name};
use crate::focus;
use crate::posters::Posters;
use crate::views::{area, band, wall};

/// What a select on a title opens. The kind decides it once, when the
/// wall opens, and no press decides it again.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Opens {
    /// A movie's page.
    Movie,
    /// A series' page.
    Series,
}

/// The wall screen: the library it draws, its titles, and where focus is.
#[derive(Debug)]
pub struct Wall {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The library's kind, which names the item table a re-read asks for.
    pub kind: String,
    /// What a select on a title opens.
    pub opens: Opens,
    /// The titles in the order the catalog answered them.
    pub items: Vec<Item>,
    /// The library's name and its count, as the band draws them. It is
    /// built at every read and not on every frame.
    pub heading: String,
    /// The focused title's index. The wall holds it while the band has
    /// focus, so down from the band returns to the same title.
    pub focus: usize,
    /// The control that holds focus, or nothing while the wall holds it.
    pub control: Option<usize>,
}

impl Wall {
    /// Read one library's titles, with focus on the first of them.
    pub fn open(library: &str, kind: &str, source: &mut dyn Source) -> Self {
        let items = read(library, kind, source);
        Self {
            library: library.to_string(),
            kind: kind.to_string(),
            opens: match kind {
                "series" => Opens::Series,
                _ => Opens::Movie,
            },
            heading: heading(library, items.len()),
            items,
            focus: 0,
            control: None,
        }
    }

    /// Read the titles again and keep focus in range, because a change
    /// can remove the focused title.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.items = read(&self.library, &self.kind, source);
        self.heading = heading(&self.library, self.items.len());
        self.focus = self.focus.min(self.items.len().saturating_sub(1));
    }

    /// Fold one press in. In the band, left and right move across the
    /// controls, select does nothing because none of the three exists
    /// yet, and down returns focus to the title the wall remembers. On
    /// the wall, up from the first row reaches the band.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if let Some(control) = self.control {
            match key {
                "down" => self.control = None,
                "enter" => {}
                _ => self.control = Some(focus::row(control, band::CONTROLS.len(), key)),
            }
            return Step::Stay;
        }
        match key {
            "enter" => self.select(source),
            "up" if self.focus < wall::COLUMNS => {
                self.control = Some(0);
                Step::Stay
            }
            _ => {
                self.focus = focus::wall(self.focus, self.items.len(), wall::COLUMNS, key);
                Step::Stay
            }
        }
    }

    /// Whether a rest of focus on this wall is worth a prefetch. It is
    /// while the posters hold focus, because a select on either kind
    /// opens a page over a backdrop.
    pub fn prefetches(&self) -> bool {
        self.control.is_none()
    }

    /// The library and the backdrop the focused title's page draws over.
    /// The browser asks the store for it once focus rests. A wall whose
    /// select opens no page over art answers nothing.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        if !self.prefetches() {
            return None;
        }
        let item = self.items.get(self.focus)?;
        let backdrop = match self.opens {
            Opens::Movie => source.movie(&self.library, &item.id)?.backdrop,
            Opens::Series => source.series(&self.library, &item.id)?.backdrop,
        };
        if backdrop.is_empty() {
            return None;
        }
        Some((self.library.clone(), backdrop))
    }

    /// The view: the band, and the wall of posters under it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        canvas(Program {
            wall: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }

    fn select(&mut self, source: &mut dyn Source) -> Step {
        let Some(item) = self.items.get(self.focus) else {
            return Step::Stay;
        };
        match self.opens {
            Opens::Movie => match movie::Movie::open(&self.library, &item.id, source) {
                Some(page) => Step::Open(Screen::Movie(Box::new(page))),
                None => Step::Stay,
            },
            Opens::Series => match series::Series::open(&self.library, &item.id, source) {
                Some(page) => Step::Open(Screen::Series(Box::new(page))),
                None => Step::Stay,
            },
        }
    }
}

fn heading(library: &str, items: usize) -> String {
    format!("{} · {}", library_name(library), items)
}

fn read(library: &str, kind: &str, source: &mut dyn Source) -> Vec<Item> {
    source
        .titles(library, kind)
        .into_iter()
        .map(Item::of)
        .collect()
}

// The wall's drawing: the band over the grid, on one frame.
struct Program<'a, P> {
    wall: &'a Wall,
    posters: &'a RefCell<P>,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Program<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        band::draw(
            &mut frame,
            bounds.width,
            &self.wall.heading,
            self.wall.control,
        );
        // The head keeps the mark of a focused slot in the first row off
        // the band above it.
        let region = area(
            0.0,
            band::HEIGHT + wall::HEAD,
            bounds.width,
            bounds.height - band::HEIGHT - wall::HEAD,
        );
        let cells = wall::cells(region.width, wall::POSTER, wall::COLUMNS);
        wall::draw(
            &mut frame,
            &mut *self.posters.borrow_mut(),
            &wall::Grid {
                items: &self.wall.items,
                focus: Some(self.wall.focus),
                marked: self.wall.control.is_none(),
                library: &self.wall.library,
                ratio: wall::POSTER,
                columns: wall::COLUMNS,
                lines: 1,
                offset: wall::scrolled(
                    self.wall.focus,
                    self.wall.items.len(),
                    wall::COLUMNS,
                    &cells,
                    region.height,
                ),
                region,
            },
        );
        vec![frame.into_geometry()]
    }
}
