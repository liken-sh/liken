//! Computes the position of every box in the section view.  Rows run
//! from top to bottom.  A row has one or more tiers of boxes, each
//! centered on the spine, the vertical line down the middle of the
//! view, and can have a column of side boxes at the left edge, the
//! right edge, or both.  A position depends only on the number of boxes
//! and the lines of text in each, so the lines between boxes are
//! computed from the same numbers that place the boxes.

/// The section view's width in pixels.  The view never reflows, because
/// the lines are drawn to the computed positions.  `section.css` zooms
/// the drawing to fit its column, and a browser without that support
/// scrolls it sideways.
pub const WIDTH: i32 = 976;
/// The x position of the spine.
pub const SPINE: i32 = 496;
/// The width of the band drawn behind the spine.
pub const SPINE_BAND: i32 = 112;
/// The width of a box alone in its tier.  A tier of several boxes, a
/// fan, gives each box `FAN_WIDTH` at most, so four boxes fit across a
/// row with no side column at full width.
const SPINE_WIDTH: i32 = 240;
const FAN_WIDTH: i32 = 192;
const FAN_GAP: i32 = 24;
/// A side box is as wide as a box in a fan.  `LEFT_X` and `RIGHT_X`
/// place the two columns at equal distances from the spine.
pub const SIDE_WIDTH: i32 = 192;
/// The most characters of a title that fit on one line of a side box:
/// the box's width less its padding and border, divided by the 0.6em
/// advance of a 13px monospace font.
pub const SIDE_TITLE_CHARS: usize = 21;
pub const LEFT_X: i32 = 72;
pub const RIGHT_X: i32 = 2 * SPINE - LEFT_X - SIDE_WIDTH;
/// The vertical gap between two boxes in a side column.
const SIDE_GAP: i32 = 16;
/// The smallest horizontal gap between a tier and a side column, which
/// leaves room for the lines between them and their labels.
pub const CLEAR: i32 = 48;
/// The smallest gap between a tier and the view's edge on a side that has
/// no side column.  A tier that would be wider narrows its boxes.
const MARGIN: i32 = 16;
/// The space above a row's first tier, for the row's label, and below
/// its last tier, for the lines that leave the row.
const ROW_PAD: i32 = 43;
const ROW_GAP: i32 = 10;
/// The vertical gap between two tiers of one row, for a line and its
/// label.
const TIER_GAP: i32 = 72;
const MIN_HEIGHT: i32 = 64;
const LINE_HEIGHT: i32 = 20;
const BOX_PAD: i32 = 24;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Rect {
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
}

impl Rect {
    pub fn center(&self) -> i32 {
        self.x + self.width / 2
    }

    pub fn middle(&self) -> i32 {
        self.y + self.height / 2
    }

    pub fn right(&self) -> i32 {
        self.x + self.width
    }

    pub fn bottom(&self) -> i32 {
        self.y + self.height
    }

    /// Whether the point at `x`, `y` is on one of its edges.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn on_edge(&self, x: i32, y: i32) -> bool {
        let across = (self.x..=self.right()).contains(&x);
        let down = (self.y..=self.bottom()).contains(&y);
        let upright = x == self.x || x == self.right();
        let level = y == self.y || y == self.bottom();
        (upright && down) || (level && across)
    }

    /// Whether the two share any point, edges included.
    #[cfg(test)]
    pub fn overlaps(&self, other: &Rect) -> bool {
        self.x <= other.right()
            && other.x <= self.right()
            && self.y <= other.bottom()
            && other.y <= self.bottom()
    }
}

/// The left or the right side column of a row.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Side {
    Left,
    Right,
}

/// Which side columns of a row the tiers must stay clear of.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Sides {
    pub left: bool,
    pub right: bool,
}

impl Sides {
    pub fn set(&mut self, side: Side) {
        match side {
            Side::Left => self.left = true,
            Side::Right => self.right = true,
        }
    }
}

/// One row as `layout` reads it, with each box given as its number of
/// lines of text, from top to bottom.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Shape {
    pub tiers: Vec<Vec<usize>>,
    pub left: Vec<usize>,
    pub right: Vec<usize>,
    /// The sides where a line from a side box to a box in another row
    /// passes through this row.  The tiers stay clear of these columns,
    /// as they do of a column that has boxes.
    pub crossed: Sides,
}

impl Shape {
    #[cfg(test)]
    pub fn tiers(tiers: Vec<Vec<usize>>) -> Self {
        Self {
            tiers,
            ..Self::default()
        }
    }

    fn clear(&self) -> Sides {
        Sides {
            left: self.crossed.left || !self.left.is_empty(),
            right: self.crossed.right || !self.right.is_empty(),
        }
    }
}

/// A tier of a row, or one of its side columns.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Column {
    Tier(usize),
    Side(Side),
}

/// The address of one box: its row, its column in the row, and its index
/// in that column.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct At {
    pub row: usize,
    pub column: Column,
    pub index: usize,
}

impl At {
    pub fn new(row: usize, tier: usize, index: usize) -> Self {
        Self {
            row,
            column: Column::Tier(tier),
            index,
        }
    }

    pub fn side(row: usize, side: Side, index: usize) -> Self {
        Self {
            row,
            column: Column::Side(side),
            index,
        }
    }

    pub fn on_side(&self) -> Option<Side> {
        match self.column {
            Column::Side(side) => Some(side),
            Column::Tier(_) => None,
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub struct Placed {
    pub top: i32,
    pub height: i32,
    pub boxes: Vec<Vec<Rect>>,
    pub left: Vec<Rect>,
    pub right: Vec<Rect>,
}

impl Placed {
    pub fn side(&self, side: Side) -> &[Rect] {
        match side {
            Side::Left => &self.left,
            Side::Right => &self.right,
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub struct Layout {
    pub width: i32,
    pub height: i32,
    pub rows: Vec<Placed>,
}

impl Layout {
    pub fn rect(&self, at: At) -> Option<Rect> {
        let row = self.rows.get(at.row)?;
        let boxes = match at.column {
            Column::Tier(tier) => row.boxes.get(tier)?.as_slice(),
            Column::Side(side) => row.side(side),
        };
        boxes.get(at.index).copied()
    }

    /// Every box in every row, to check that no two overlap.
    #[cfg(test)]
    pub fn all(&self) -> Vec<Rect> {
        let row = |row: &Placed| -> Vec<Rect> {
            let tiers = row.boxes.iter().flatten();
            tiers.chain(&row.left).chain(&row.right).copied().collect()
        };
        self.rows.iter().flat_map(row).collect()
    }
}

/// Returns the position of every box in `rows`.  A tier is as tall as
/// its tallest box, so the boxes of a tier share a top and a bottom and
/// the lines between two tiers have the same length.  The tiers and each
/// side column are centered vertically in the row, so a side box beside
/// a lone workload is level with it.
pub fn layout(rows: &[Shape]) -> Layout {
    let mut top = 0;
    let mut placed = Vec::with_capacity(rows.len());
    for (index, shape) in rows.iter().enumerate() {
        if index > 0 {
            top += ROW_GAP;
        }
        let heights: Vec<i32> = shape.tiers.iter().map(|tier| tier_height(tier)).collect();
        let gaps = TIER_GAP * (i32::try_from(heights.len()).unwrap_or(1) - 1).max(0);
        let tiers = heights.iter().sum::<i32>() + gaps;
        let content = tiers
            .max(stack_height(&shape.left))
            .max(stack_height(&shape.right));
        let first = top + ROW_PAD;
        let clear = shape.clear();
        let mut y = first + (content - tiers) / 2;
        let mut boxes = Vec::with_capacity(heights.len());
        for (tier, height) in shape.tiers.iter().zip(heights) {
            boxes.push(across(tier.len(), y, height, clear));
            y += height + TIER_GAP;
        }
        let left = stack(&shape.left, LEFT_X, first, content);
        let right = stack(&shape.right, RIGHT_X, first, content);
        let height = 2 * ROW_PAD + content;
        placed.push(Placed {
            top,
            height,
            boxes,
            left,
            right,
        });
        top += height;
    }
    Layout {
        width: WIDTH,
        height: top,
        rows: placed,
    }
}

fn box_height(lines: usize) -> i32 {
    let lines = i32::try_from(lines).unwrap_or(i32::MAX / LINE_HEIGHT);
    (BOX_PAD + lines * LINE_HEIGHT).max(MIN_HEIGHT)
}

fn tier_height(tier: &[usize]) -> i32 {
    let height = tier.iter().map(|&lines| box_height(lines)).max();
    height.unwrap_or(MIN_HEIGHT)
}

fn stack_height(boxes: &[usize]) -> i32 {
    let heights = boxes.iter().map(|&lines| box_height(lines) + SIDE_GAP);
    (heights.sum::<i32>() - SIDE_GAP).max(0)
}

/// Stacks `boxes` at `x`, centered vertically in a row whose content
/// starts at `first` and is `content` tall.
fn stack(boxes: &[usize], x: i32, first: i32, content: i32) -> Vec<Rect> {
    let mut y = first + (content - stack_height(boxes)) / 2;
    boxes
        .iter()
        .map(|&lines| {
            let height = box_height(lines);
            let rect = Rect {
                x,
                y,
                width: SIDE_WIDTH,
                height,
            };
            y += height + SIDE_GAP;
            rect
        })
        .collect()
}

/// Places `count` boxes side by side at `y`, centered on the spine.  A
/// tier that must stay clear of a side column ends `CLEAR` short of it.
/// The tier moves away from that column first, and narrows its boxes
/// only when moving is not enough.
fn across(count: usize, y: i32, height: i32, clear: Sides) -> Vec<Rect> {
    let Ok(n) = i32::try_from(count) else {
        return Vec::new();
    };
    let low = match clear.left {
        true => LEFT_X + SIDE_WIDTH + CLEAR,
        false => MARGIN,
    };
    let high = match clear.right {
        true => RIGHT_X - CLEAR,
        false => WIDTH - MARGIN,
    };
    let width = match n {
        0 => return Vec::new(),
        1 => SPINE_WIDTH,
        _ => FAN_WIDTH.min((high - low - (n - 1) * FAN_GAP) / n),
    };
    let total = n * width + (n - 1) * FAN_GAP;
    let mut left = SPINE - total / 2;
    if clear.right {
        left = left.min(high - total);
    }
    if clear.left {
        left = left.max(low);
    }
    (0..n)
        .map(|i| Rect {
            x: left + i * (width + FAN_GAP),
            y,
            width,
            height,
        })
        .collect()
}

#[cfg(test)]
mod tests;
