// The grid of letters a remote with no keyboard types on. Every cell is
// a browser word, so a pick off the grid and a press on a physical
// keyboard reach the field by one path. The grid draws the way a wall
// does: rounded cells, and the focus mark on the one that holds focus.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle};

use super::{area, mark, rounded, text};
use crate::focus;
use crate::look;

/// Every cell of the grid in reading order, each as the browser word a
/// pick on it presses.
pub const CELLS: [&str; 38] = [
    "a",
    "b",
    "c",
    "d",
    "e",
    "f",
    "g",
    "h",
    "i",
    "j",
    "k",
    "l",
    "m",
    "n",
    "o",
    "p",
    "q",
    "r",
    "s",
    "t",
    "u",
    "v",
    "w",
    "x",
    "y",
    "z",
    "0",
    "1",
    "2",
    "3",
    "4",
    "5",
    "6",
    "7",
    "8",
    "9",
    " ",
    "backspace",
];

/// How many cells a row holds. The count is fixed, like a wall's, so the
/// focus arithmetic and the layout agree.
pub const COLUMNS: usize = 10;

/// The grid and the cell that holds focus.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Keyboard {
    focus: usize,
}

impl Keyboard {
    /// Fold one press in. The arrows move focus across the grid, and
    /// every other word leaves it. The answer is whether focus moved, so
    /// the caller draws a frame only when the grid changed.
    pub fn key(&mut self, word: &str) -> bool {
        let moved = focus::wall(self.focus, CELLS.len(), COLUMNS, word);
        let changed = moved != self.focus;
        self.focus = moved;
        changed
    }

    /// The word of the focused cell, which the caller presses into the
    /// field.
    pub fn pick(&self) -> &'static str {
        CELLS[self.focus]
    }
}

// One cell's width and height, the gap between two, and the radius a
// cell rounds by. A cell has to hold "backspace", the widest word shown,
// at the control size, and ten cells have to fit under the band on a
// 1920-wide frame.
const CELL: f32 = 96.0;
const HEIGHT: f32 = 72.0;
const GAP: f32 = 12.0;
const RADIUS: f32 = 8.0;

// How many rows the cells fill. The last row is short, and its cells
// keep the width of every other cell.
fn rows() -> usize {
    CELLS.len().div_ceil(COLUMNS)
}

/// The width of the whole grid, which the caller centers it by.
pub fn width() -> f32 {
    COLUMNS as f32 * CELL + (COLUMNS - 1) as f32 * GAP
}

/// The height of the whole grid.
pub fn height() -> f32 {
    rows() as f32 * HEIGHT + (rows() - 1) as f32 * GAP
}

/// One cell's box, in a grid whose first cell's corner is `at`.
pub fn cell(at: Point, index: usize) -> Rectangle {
    let column = (index % COLUMNS) as f32;
    let row = (index / COLUMNS) as f32;
    area(
        at.x + column * (CELL + GAP),
        at.y + row * (HEIGHT + GAP),
        CELL,
        HEIGHT,
    )
}

/// The label a cell shows. The space is the one cell whose word is not
/// readable as text.
pub fn shown(word: &str) -> &str {
    match word {
        " " => "space",
        word => word,
    }
}

/// Draw the grid with its first cell's corner at `at`.
pub fn draw(frame: &mut canvas::Frame<Renderer>, at: Point, keyboard: &Keyboard) {
    for (index, word) in CELLS.iter().enumerate() {
        let bounds = cell(at, index);
        frame.fill(&rounded(bounds, RADIUS), look::slot());
        text::centered(
            frame,
            shown(word),
            band(bounds),
            look::CONTROL,
            look::text(),
        );
        if index == keyboard.focus {
            mark(frame, bounds);
        }
    }
}

// The line a cell's label draws on, centered in the cell.
fn band(cell: Rectangle) -> Rectangle {
    let line = text::height(1, look::CONTROL);
    area(cell.x, cell.center_y() - line / 2.0, cell.width, line)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_grid_holds_every_letter_every_digit_the_space_and_the_backspace() {
        let letters: Vec<&str> = CELLS[..26].to_vec();
        let expected: Vec<String> = (b'a'..=b'z')
            .map(|byte| (byte as char).to_string())
            .collect();
        assert_eq!(letters, expected);
        let digits: Vec<&str> = CELLS[26..36].to_vec();
        assert_eq!(digits, ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"]);
        assert_eq!(CELLS[36], " ");
        assert_eq!(CELLS[37], "backspace");
    }

    // One case: the cell that holds focus, and the word a pick on it
    // presses.
    const PICKS: [(usize, &str); 6] = [
        (0, "a"),
        (9, "j"),
        (25, "z"),
        (26, "0"),
        (36, " "),
        (37, "backspace"),
    ];

    #[test]
    fn every_cell_picks_the_word_it_carries() {
        for (focus, word) in PICKS {
            let keyboard = Keyboard { focus };
            assert_eq!(keyboard.pick(), word, "{focus}");
        }
    }

    // One case: the cell that held focus, the word pressed, and the cell
    // that holds focus after.
    const MOVES: [(usize, &str, usize); 12] = [
        (0, "right", 1),
        (9, "right", 10),
        (10, "left", 9),
        (0, "left", 0),
        (37, "right", 37),
        (0, "down", 10),
        (10, "up", 0),
        (5, "up", 5),
        (29, "down", 37),
        (35, "down", 35),
        (37, "up", 27),
        (4, "enter", 4),
    ];

    #[test]
    fn the_arrows_move_focus_across_the_grid_and_clamp_at_its_edges() {
        for (from, word, to) in MOVES {
            let mut keyboard = Keyboard { focus: from };
            assert_eq!(keyboard.key(word), from != to, "{from} {word}");
            assert_eq!(keyboard.focus, to, "{from} {word}");
        }
    }

    #[test]
    fn a_new_keyboard_holds_focus_on_the_first_cell() {
        assert_eq!(Keyboard::default().pick(), "a");
    }

    #[test]
    fn the_space_cell_shows_a_word_and_every_other_cell_shows_itself() {
        assert_eq!(shown(" "), "space");
        assert_eq!(shown("a"), "a");
        assert_eq!(shown("backspace"), "backspace");
    }

    #[test]
    fn the_cells_sit_in_their_column_and_row() {
        let at = Point::new(120.0, 400.0);
        let first = cell(at, 0);
        assert_eq!(first.x, at.x);
        assert_eq!(first.y, at.y);
        assert_eq!(first.width, CELL);
        assert_eq!(first.height, HEIGHT);

        let beside = cell(at, 1);
        assert_eq!(beside.x, first.x + CELL + GAP);
        assert_eq!(beside.y, first.y);

        let below = cell(at, COLUMNS);
        assert_eq!(below.x, first.x);
        assert_eq!(below.y, first.y + HEIGHT + GAP);
    }

    #[test]
    fn the_grid_is_as_wide_and_as_tall_as_the_cells_it_holds() {
        assert_eq!(rows(), 4);
        let last = cell(Point::ORIGIN, COLUMNS - 1);
        assert_eq!(width(), last.x + last.width);
        let bottom = cell(Point::ORIGIN, CELLS.len() - 1);
        assert_eq!(height(), bottom.y + bottom.height);
    }

    #[test]
    fn a_cells_word_draws_on_one_line_inside_the_cell() {
        let cell = cell(Point::new(0.0, 0.0), 0);
        let band = band(cell);
        assert_eq!(band.height, text::height(1, look::CONTROL));
        assert_eq!(band.width, cell.width);
        assert_eq!(band.center_y(), cell.center_y());
        assert!(band.y > cell.y);
    }

    #[test]
    fn the_widest_word_a_cell_shows_fits_inside_it() {
        for word in CELLS {
            assert!(
                text::measured(shown(word), look::CONTROL) < CELL,
                "{}",
                shown(word)
            );
        }
    }
}
