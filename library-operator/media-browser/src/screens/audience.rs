// The picker the browser draws over the stack to ask who is watching: one
// tile per known person, a link under the row that answers, and the presses
// that move focus, toggle a person, and answer. The layout is pure functions
// of the frame's bounds and the number of tiles, so the placement is tested
// with numbers and no window.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use crate::audience::Person;
use crate::look;
use crate::views::{area, extent, mark, text};

/// The word on the link while no person is chosen. The link then answers
/// that nobody is watching.
pub const NOBODY: &str = "Nobody";

/// The word on the link once a person is chosen. The link then answers the
/// people the row holds.
pub const START: &str = "Start";

/// The side of the square one tile's circle draws in.
pub const CIRCLE: f32 = 160.0;

// The space between two tiles, wider than the focus mark reaches.
const GAP: f32 = 48.0;

// The space between the heading and the row of tiles.
const HEADING_GAP: f32 = 48.0;

// The space between a circle and the name under it.
const NAME_GAP: f32 = 16.0;

// The size of the letter inside a circle.
const LETTER: f32 = 64.0;

// The space between the names under the row and the link under them.
const LINK_GAP: f32 = 48.0;

// The padding around the link's word, inside the box the focus mark goes
// around.
const LINK_PAD: f32 = 12.0;

/// The distance from one tile to the next.
pub fn pitch() -> f32 {
    CIRCLE + GAP
}

// The height of the box the link draws in: one line of the caption face
// and the padding around it.
fn link_height() -> f32 {
    text::height(1, look::CAPTION) + 2.0 * LINK_PAD
}

// The height the heading, the circles, the names, and the link take
// together, so the block centres in the frame.
fn height() -> f32 {
    text::height(1, look::TITLE)
        + HEADING_GAP
        + CIRCLE
        + NAME_GAP
        + text::height(1, look::CAPTION)
        + LINK_GAP
        + link_height()
}

// The top of the block.
fn top(bounds: Rectangle) -> f32 {
    bounds.y + (bounds.height - height()) / 2.0
}

/// The band the heading draws in, across the frame.
pub fn heading(bounds: Rectangle) -> Rectangle {
    area(
        bounds.x,
        top(bounds),
        bounds.width,
        text::height(1, look::TITLE),
    )
}

// The top of the row of circles, under the heading.
fn row_top(bounds: Rectangle) -> f32 {
    top(bounds) + text::height(1, look::TITLE) + HEADING_GAP
}

/// The square one tile's circle draws in, in a row centered across the
/// frame.
pub fn tile(bounds: Rectangle, tiles: usize, index: usize) -> Rectangle {
    let row = tiles as f32 * CIRCLE + tiles.saturating_sub(1) as f32 * GAP;
    let left = bounds.x + (bounds.width - row) / 2.0;
    area(
        left + index as f32 * pitch(),
        row_top(bounds),
        CIRCLE,
        CIRCLE,
    )
}

/// The box the link draws in: under the names, centered across the frame,
/// as wide as its word and the padding around it.
pub fn link(bounds: Rectangle, word: &str) -> Rectangle {
    let width = text::measured(word, look::CAPTION) + 2.0 * LINK_PAD;
    area(
        bounds.x + (bounds.width - width) / 2.0,
        row_top(bounds) + CIRCLE + NAME_GAP + text::height(1, look::CAPTION) + LINK_GAP,
        width,
        link_height(),
    )
}

/// The band the link's word draws in, inside the box the focus mark goes
/// around.
pub fn word_at(link: Rectangle) -> Rectangle {
    area(
        link.x,
        link.y + LINK_PAD,
        link.width,
        text::height(1, look::CAPTION),
    )
}

/// The band the name under one tile draws in, as wide as the pitch, so no
/// name runs under its neighbor's.
pub fn name(tile: Rectangle) -> Rectangle {
    area(
        tile.center_x() - pitch() / 2.0,
        tile.y + tile.height + NAME_GAP,
        pitch(),
        text::height(1, look::CAPTION),
    )
}

/// What the picker's focus stands on: one tile of the row, or the link
/// under it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    /// The tile at this index in the known list.
    Tile(usize),
    /// The link under the row, which answers.
    Link,
}

/// The picker's own state: which of the known people are chosen, which
/// tile focus was last on, and whether it stands on the link. The people
/// themselves stay with the browser's audience, so the picker names them
/// by index alone.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Picker {
    chosen: Vec<bool>,
    tile: usize,
    on_link: bool,
}

impl Picker {
    /// The picker over this many known people, with these of them chosen
    /// and focus on the first tile.
    pub fn open(people: usize, chosen: &[usize]) -> Self {
        let mut holds = vec![false; people];
        for index in chosen {
            if let Some(hold) = holds.get_mut(*index) {
                *hold = true;
            }
        }
        Self {
            chosen: holds,
            tile: 0,
            on_link: false,
        }
    }

    /// How many tiles the row holds: one per known person.
    pub fn tiles(&self) -> usize {
        self.chosen.len()
    }

    /// What holds focus: one tile of the row, or the link.
    pub fn focus(&self) -> Focus {
        match self.on_link {
            true => Focus::Link,
            false => Focus::Tile(self.tile),
        }
    }

    /// Whether nobody has moved inside the picker yet: focus on the first
    /// tile and nobody chosen, which is how a picker the ask raised
    /// stands. An answer that arrives from elsewhere may close such a
    /// picker, because nobody is part way through an answer of their own.
    pub fn untouched(&self) -> bool {
        !self.on_link && self.tile == 0 && !self.chosen.iter().any(|chosen| *chosen)
    }

    /// Whether the person at this index is chosen.
    pub fn holds(&self, index: usize) -> bool {
        self.chosen.get(index).copied().unwrap_or_default()
    }

    /// The word on the link: Nobody while no person is chosen, Start once
    /// one is.
    pub fn word(&self) -> &'static str {
        match self.chosen.iter().any(|chosen| *chosen) {
            true => START,
            false => NOBODY,
        }
    }

    /// Fold one press. The answer is the people the press chose, by their
    /// index in the known list, or nothing while the picker still stands.
    /// Enter on a person toggles them. Down reaches the link, and up returns
    /// to the tile focus left. Enter on the link and back both answer
    /// whichever people are chosen.
    pub fn key(&mut self, name: &str) -> Option<Vec<usize>> {
        match (name, self.on_link) {
            ("left", false) => {
                self.tile = self.tile.saturating_sub(1);
                None
            }
            ("right", false) => {
                self.tile = (self.tile + 1).min(self.tiles().saturating_sub(1));
                None
            }
            ("down", false) => {
                self.on_link = true;
                None
            }
            ("up", true) => {
                self.on_link = false;
                None
            }
            ("enter", false) => {
                if let Some(chosen) = self.chosen.get_mut(self.tile) {
                    *chosen = !*chosen;
                }
                None
            }
            ("enter", true) | ("escape" | "backspace", _) => Some(self.answer()),
            _ => None,
        }
    }

    // The people that are chosen, by their index in the known list.
    fn answer(&self) -> Vec<usize> {
        self.chosen
            .iter()
            .enumerate()
            .filter(|(_, chosen)| **chosen)
            .map(|(index, _)| index)
            .collect()
    }
}

/// The picker as one layer over the stack: the shade that dims what is
/// under it, the heading, and the row of tiles.
pub struct Layer<'a> {
    /// The people the picker offers, in the order the list came.
    pub people: &'a [Person],
    /// What is chosen and where focus stands.
    pub picker: &'a Picker,
}

impl Layer<'_> {
    // The name under one tile: the person's display name.
    fn caption(&self, index: usize) -> &str {
        self.people
            .get(index)
            .map_or("", |person| person.display_name.as_str())
    }
}

/// The question the picker asks.
pub const QUESTION: &str = "Who is watching?";

impl canvas::Program<Infallible, Theme, Renderer> for Layer<'_> {
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
        frame.fill_rectangle(bounds.position(), extent(bounds), look::shade());
        text::centered(
            &mut frame,
            QUESTION,
            heading(bounds),
            look::TITLE,
            look::text(),
        );

        let tiles = self.picker.tiles();
        for index in 0..tiles {
            let at = tile(bounds, tiles, index);
            let caption = self.caption(index);
            let centre = Point::new(at.center_x(), at.center_y());
            let radius = at.width / 2.0;
            frame.fill(&canvas::Path::circle(centre, radius), look::slot());
            if self.picker.holds(index) {
                frame.stroke(
                    &canvas::Path::circle(centre, radius - look::MARK / 2.0),
                    canvas::Stroke::default()
                        .with_color(look::mark())
                        .with_width(look::MARK),
                );
            }
            text::shown(
                &mut frame,
                &caption.chars().next().unwrap_or_default().to_string(),
                area(
                    at.x,
                    at.center_y() - text::height(1, LETTER) / 2.0,
                    at.width,
                    text::height(1, LETTER),
                ),
                LETTER,
                look::text(),
            );
            text::centered(&mut frame, caption, name(at), look::CAPTION, look::text());
            if self.picker.focus() == Focus::Tile(index) {
                mark(&mut frame, at);
            }
        }

        let word = self.picker.word();
        let link = link(bounds, word);
        text::shown(&mut frame, word, word_at(link), look::CAPTION, look::text());
        if self.picker.focus() == Focus::Link {
            mark(&mut frame, link);
        }

        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests;
