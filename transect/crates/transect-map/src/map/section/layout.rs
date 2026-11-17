//! The position of each box in the section view.  The section view has a
//! fixed layout: its rows run from top to bottom, each row is a stack of
//! tiers centered on the spine, and a row can have a column of side boxes
//! at either edge.  Positions are computed only from the count of boxes
//! and their lines of text, so the same section view always gets the same
//! positions, and the lines can be computed from the same numbers that
//! the boxes are drawn at.

/// The section view's width.  A narrower window scrolls sideways and
/// does not wrap, because the lines are drawn to the computed positions
/// and a wrapped box would move away from its lines.
pub const WIDTH: i32 = 976;
/// The spine's center, where a tier of one box is centered.
pub const SPINE: i32 = 496;
/// The width of the band drawn behind the spine.
pub const SPINE_BAND: i32 = 112;
/// A tier of one box draws it wide.  A tier of several boxes, a fan,
/// draws each one narrower, so four machines fit across the section view.
const SPINE_WIDTH: i32 = 240;
const FAN_WIDTH: i32 = 192;
const FAN_GAP: i32 = 24;
/// The side columns are as wide as a box in a fan, and the left column is
/// as far from the spine as the right column.
pub const SIDE_WIDTH: i32 = 192;
/// The most characters of a mono title that fit on one line of a side
/// box: its width less its padding and border, at 13px IBM Plex Mono's
/// advance of 0.6em.
pub const SIDE_TITLE_CHARS: usize = 21;
pub const LEFT_X: i32 = 72;
pub const RIGHT_X: i32 = 2 * SPINE - LEFT_X - SIDE_WIDTH;
/// Room between two boxes stacked in a side column.
const SIDE_GAP: i32 = 16;
/// The least room between a fan and a side column that the fan keeps
/// clear, so that the lines that run between them, and their labels, fit.
pub const CLEAR: i32 = 48;
/// The least room at each side of a tier that is too wide for its boxes
/// at full width.  Such a tier narrows its boxes to keep this room.
const MARGIN: i32 = 16;
/// The room above a row's first tier, for the row's label, and below its
/// last tier, for the lines that leave the row.
const ROW_PAD: i32 = 43;
const ROW_GAP: i32 = 10;
/// Room between two tiers of one row for a line and its label.
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

    /// Whether the point at `x`, `y` lies on one of its edges.
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

/// Either side column.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Side {
    Left,
    Right,
}

/// Which sides of a row its tiers keep clear.
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

/// One row as the layout reads it, with each box given as its count of
/// lines of text: its tiers top to bottom, and its side columns top to
/// bottom.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Shape {
    pub tiers: Vec<Vec<usize>>,
    pub left: Vec<usize>,
    pub right: Vec<usize>,
    /// The sides a line runs through the row on, between a side box and
    /// a box in another row.  The tiers keep clear of those columns, as
    /// they do of a column that holds a box.
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

/// A place in a row: one of its tiers, or a side column.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Column {
    Tier(usize),
    Side(Side),
}

/// One box, by its row, its column in the row, and its index in the
/// column.
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
    /// Each tier's boxes, in the shape's order.
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

    /// Every box, for the checks that no two overlap.
    #[cfg(test)]
    pub fn all(&self) -> Vec<Rect> {
        let row = |row: &Placed| -> Vec<Rect> {
            let tiers = row.boxes.iter().flatten();
            tiers.chain(&row.left).chain(&row.right).copied().collect()
        };
        self.rows.iter().flat_map(row).collect()
    }
}

/// The position of every box in `rows`.  A tier is as tall as its
/// tallest box, so the boxes of a tier share a top and a bottom and the
/// lines between two tiers run the same length.  The tiers and each
/// side column are centered on the row's height, so a side box beside a
/// lone workload is level with it.
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

/// `boxes` one above another at `x`, centered on a row's `content`
/// height that starts at `first`.
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

/// `count` boxes side by side at `y`, centered on the spine.  A tier
/// that keeps a side clear ends `CLEAR` short of that column.  It moves
/// toward the other side first, and narrows its boxes only when it must,
/// so four machines and a held device fit in the machines row.
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
