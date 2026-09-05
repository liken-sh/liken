// The slots one query answers, as a wall of art under a band. The three
// controls for sort, filter, and search reserve their place at the band's
// right. Up from the first row of posters reaches the band, and down from
// the band returns focus to the slot the wall remembers.
//
// A wall with a page of its own carries a head between the band and the
// grid: the name of what the wall is about, and the counts of its slots
// by kind. The band over such a wall carries the query's kind word and
// not its heading, so the name reads once.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Point, Rectangle, Theme, mouse};

use super::Step;
use super::slots::Slots;
use super::{Item, facts};
use crate::catalog::{Query, Source};
use crate::focus;
use crate::look;
use crate::posters::Posters;
use crate::views::stack::Stack;
use crate::views::{area, band, card, text, wall};

// The margin at both sides of the head, the person page's own, so the
// two heads line up.
const MARGIN: f32 = 120.0;

// The space over the head's name, the space under its facts before the
// grid's region, and the space between the two lines.
const TOP: f32 = 56.0;
const FOOT: f32 = 36.0;
const GAP: f32 = 12.0;

/// What a wall with a page of its own draws over the grid: the name of
/// what the wall is about, and the counts of its slots by kind. It is a
/// function of the query and its answer, so the wall for a genre is the
/// genre's page wherever it opens from.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Head {
    pub name: String,
    pub facts: String,
}

impl Head {
    // The head over these slots, read off the query and its answer.
    fn of(query: &Query, name: &str, items: &[Item]) -> Self {
        let of_kind = |kind: &str| items.iter().filter(|item| item.kind == kind).count();
        Self {
            name: query.name(name),
            facts: counted(of_kind("movies"), of_kind("series")),
        }
    }
}

// How many movies and how many series the wall holds, on one line. A
// kind the wall holds none of leaves no words behind.
fn counted(movies: usize, series: usize) -> String {
    let counted = |count: usize, nouns| match count {
        0 => String::new(),
        count => facts::counted(count as i64, nouns),
    };
    facts::joined(&[&counted(movies, "movies"), &counted(series, "series")])
}

/// The wall screen: the slots the query answered, the head over the
/// grid or nothing where the query has no page of its own, the heading
/// as the band draws it, and the control that holds focus, or nothing
/// while the slots hold it. The head and the heading are built at every
/// read and not on every frame.
#[derive(Debug)]
pub struct Wall {
    pub slots: Slots,
    pub head: Option<Head>,
    pub heading: String,
    pub control: Option<usize>,
}

impl Wall {
    /// Read the query's slots, with focus on the first of them.
    pub fn open(query: Query, source: &mut dyn Source) -> Self {
        let mut wall = Self {
            slots: Slots::open(query, source),
            head: None,
            heading: String::new(),
            control: None,
        };
        wall.headed();
        wall
    }

    /// Read the titles again and keep focus in range, because a change
    /// can remove the focused title.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.slots.reread(source);
        self.headed();
    }

    // The head and the heading, both functions of the slots the read
    // answered. A query with a page of its own puts its name in the head
    // and its kind word in the band. Every other query heads nothing and
    // puts its own heading in the band.
    fn headed(&mut self) {
        let (head, heading) = match self.slots.query.kind_word() {
            Some(word) => (
                Some(Head::of(
                    &self.slots.query,
                    &self.slots.name,
                    &self.slots.items,
                )),
                word.to_string(),
            ),
            None => (None, self.slots.heading()),
        };
        self.head = head;
        self.heading = heading;
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
        if key == "up" && self.slots.focus < wall::COLUMNS {
            self.control = Some(0);
            return Step::Stay;
        }
        self.slots.key(key, source)
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
        self.slots.resting(source)
    }

    /// The view: the head and the wall of posters, and the band as a layer
    /// over them.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        let grid = canvas(Program {
            wall: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into();
        let band = band::layer(&self.heading, &band::CONTROLS, self.control);
        iced_widget::Stack::with_children(vec![grid, band])
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }
}

// The wall's drawing under the band: the head and the grid, on one frame.
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
        if let Some(head) = &self.wall.head {
            words(&mut frame, head, bounds.width);
        }
        let region = region(bounds, self.wall.head.as_ref());
        self.wall.slots.draw(
            &mut frame,
            &mut *self.posters.borrow_mut(),
            region,
            self.wall.control.is_none(),
            card::LINES,
        );
        vec![frame.into_geometry()]
    }
}

// The head's two lines: the name at the title size, and the counts
// under it, muted, the way the person page draws the words beside the
// headshot.
fn words(frame: &mut canvas::Frame<Renderer>, head: &Head, width: f32) {
    let column = width - MARGIN * 2.0;
    let mut stack = Stack::new(Point::new(MARGIN, band::HEIGHT + TOP), GAP);
    for (content, size, color) in [
        (&head.name, look::TITLE, look::text()),
        (&head.facts, look::FACTS, look::muted()),
    ] {
        let taken = text::block(frame, content, stack.at(), size, color, column, 1);
        stack.add(taken);
    }
}

// The part of the frame the grid scrolls in: under the band, under the
// head where the wall carries one, and under the space that keeps the
// mark of a focused slot in the first row off what is over it.
fn region(bounds: Rectangle, head: Option<&Head>) -> Rectangle {
    let top = band::HEIGHT + height(head);
    area(
        0.0,
        top + wall::HEAD,
        bounds.width,
        bounds.height - top - wall::HEAD,
    )
}

// The height a head takes, whatever its words, so the grid starts at
// the same place on every headed wall. A wall with no head takes none.
fn height(head: Option<&Head>) -> f32 {
    match head {
        Some(_) => TOP + text::height(1, look::TITLE) + GAP + text::height(1, look::FACTS) + FOOT,
        None => 0.0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;

    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    fn frame() -> Rectangle {
        area(0.0, 0.0, WIDTH, HEIGHT)
    }

    fn head() -> Head {
        Head {
            name: "Western".into(),
            facts: "7 movies · 3 series".into(),
        }
    }

    #[test]
    fn a_head_pushes_the_grid_down_and_a_wall_without_one_starts_under_the_band() {
        let bare = region(frame(), None);
        assert_eq!(bare.y, band::HEIGHT + wall::HEAD);
        assert_eq!(bare.y + bare.height, HEIGHT);

        let headed = region(frame(), Some(&head()));
        assert!(headed.y > bare.y, "{headed:?}");
        assert_eq!(headed.y + headed.height, HEIGHT);
        assert!(headed.y < HEIGHT / 2.0, "{headed:?}");
    }

    #[test]
    fn a_row_of_posters_fits_under_the_head() {
        let cells = wall::lined(WIDTH, wall::POSTER, wall::COLUMNS, 1);
        assert!(cells.height <= region(frame(), Some(&head())).height);
    }

    #[test]
    fn the_counts_leave_out_a_kind_the_wall_holds_none_of() {
        let cases = [
            (0, 0, ""),
            (7, 0, "7 movies"),
            (0, 3, "3 series"),
            (1, 1, "1 movie · 1 series"),
            (7, 3, "7 movies · 3 series"),
        ];
        for (movies, series, want) in cases {
            assert_eq!(counted(movies, series), want, "{movies} and {series}");
        }
    }

    #[test]
    fn a_wall_whose_band_holds_focus_prefetches_nothing() {
        let query = Query::Library {
            library: "sample/features".into(),
        };
        let mut wall = Wall::open(query, &mut Catalog);
        wall.control = Some(0);
        assert!(!wall.prefetches());
        assert_eq!(wall.resting(&mut Catalog), None);
    }
}
