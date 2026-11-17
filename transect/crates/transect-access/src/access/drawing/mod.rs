//! A workload's access drawing shows what may reach each port that the
//! workload serves, what the workload runs as, holds, and may call, and
//! who may change it.  It is the same kind of drawing as the section
//! view: the server places the boxes, and the lines go under them in
//! one SVG at the same coordinates, drawn with the section view's box
//! macro and line paths.  The drawing goes from left to right, from
//! what reaches the workload to the workload, and from the workload to
//! what it holds and calls.

pub mod account;
pub mod account_page;
pub mod change;
pub mod page;
pub mod reach;
pub mod right;
mod route;
mod sockets;
#[cfg(test)]
mod tests;

use transect_map::map::section_drawing::{Line, Point, Rect, SectionBox, Style, rounded};

/// The width that every access drawing is laid out at.  The page zooms
/// the drawing to the width of its column, as the section view does.
pub const WIDTH: i32 = 1280;

/// A drawing laid out: its boxes, the lines under them, the gates on
/// the lines, its words, and the ports on the workload's edge.
#[derive(Debug, Default)]
pub struct Drawing {
    pub width: i32,
    pub height: i32,
    pub boxes: Vec<Placed>,
    pub lines: Vec<Line>,
    pub dots: Vec<Dot>,
    pub texts: Vec<Text>,
    pub sockets: Vec<Socket>,
    /// The turns of each line, in the order of `lines`.
    pub routes: Vec<Vec<Point>>,
}

/// One box at its place, with the class it is drawn with.
#[derive(Debug)]
pub struct Placed {
    pub boxed: SectionBox,
    pub class: &'static str,
    pub rect: Rect,
}

impl Placed {
    /// The inline style that puts it at its place.
    pub fn place(&self) -> String {
        style(&self.rect)
    }
}

/// A gate on a line, as a dot: its class, the sentence it shows on
/// hover, and whether a sentence declares it.  A declared gate is drawn
/// with a ring.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Gate {
    pub class: &'static str,
    pub title: String,
    pub declared: bool,
}

/// A gate placed on a line.
#[derive(Debug, PartialEq, Eq)]
pub struct Dot {
    pub x: i32,
    pub y: i32,
    pub gate: Gate,
}

/// Words in the drawing: a column's heading, or a line's label.
#[derive(Debug, PartialEq, Eq)]
pub struct Text {
    pub x: i32,
    pub y: i32,
    pub text: String,
    pub class: &'static str,
    /// The page that the words link to, when they are a name.
    pub href: Option<String>,
}

/// A port the workload serves, on its box's left edge.
#[derive(Debug, PartialEq, Eq)]
pub struct Socket {
    pub rect: Rect,
    pub text: String,
    pub title: String,
}

impl Socket {
    pub fn place(&self) -> String {
        style(&self.rect)
    }
}

/// A column's heading, small and in capitals.
pub const HEAD: &str = "section-label section-label-start ax-head";
/// A line's label, starting at its point.
pub const LABEL: &str = "section-label section-label-start";
/// A policy's name before the gate it puts on a line, ending at its
/// point.
pub const VIA: &str = "section-label section-label-end ax-via";
/// A box with no style of its own.
pub const PLAIN: &str = "section-box";
/// The workload the drawing is of.
pub const WORKLOAD: &str = "section-box section-box-workload ax-box-ports ax-box-list";
/// Something that follows from a grant.
pub const FOLLOWS: &str = "section-box ax-box-follows";
/// A stated "none".
pub const NONE: &str = "section-box ax-box-none";

fn style(rect: &Rect) -> String {
    format!(
        "left: {}px; top: {}px; width: {}px; height: {}px",
        rect.x, rect.y, rect.width, rect.height
    )
}

/// The height that a box of `width` needs for its title and lines, each
/// wrapped whole.  The character widths are those of 12.5px bold mono
/// and 13px Source Sans 3 in a browser, measured a little wide so that
/// a box is too tall rather than too short.  A box grows to fit all of
/// its words, so none is cut short.
pub fn height(boxed: &SectionBox, width: i32) -> i32 {
    let inner = (width - 22).max(40);
    let title_chars = (inner * 10 / 85).max(1) as usize;
    let line_chars = (inner * 10 / 66).max(1) as usize;
    let mut count = wrapped(&boxed.title, title_chars);
    let names: Vec<&str> = boxed.names.iter().map(|part| part.text.as_str()).collect();
    let named = names.join(", ");
    for (at, line) in boxed.lines.iter().enumerate() {
        // A box that represents several names lists them in its last
        // line in place of that line's words.
        let line = match at + 1 == boxed.lines.len() && !names.is_empty() {
            true => named.as_str(),
            false => line.as_str(),
        };
        let extra = if boxed.mark.is_some() { 8 } else { 0 };
        count += wrapped(&format!("{}{line}", "x".repeat(extra)), line_chars);
    }
    let blocks = match boxed.blocks.is_empty() {
        true => 0,
        false => 1,
    };
    28 + 18 * (count + blocks) as i32
}

/// How many lines `text` takes at `most` characters a line, wrapped
/// as a browser wraps it: after a space, a hyphen, a dot, or a slash,
/// and inside a piece only when the piece alone is longer than a line.
fn wrapped(text: &str, most: usize) -> usize {
    let mut lines = 1;
    let mut used = 0;
    for piece in text.split_inclusive([' ', '-', '.', '/']) {
        let len = piece.trim_end_matches(' ').chars().count();
        let room = piece.chars().count();
        if used > 0 && used + len > most {
            lines += 1;
            used = 0;
        }
        if len > most {
            lines += (len - 1) / most;
            used = len % most;
        } else {
            used += room;
        }
    }
    lines
}

impl Drawing {
    fn new() -> Self {
        Self {
            width: WIDTH,
            ..Self::default()
        }
    }

    fn place(&mut self, boxed: SectionBox, class: &'static str, rect: Rect) -> Rect {
        self.boxes.push(Placed { boxed, class, rect });
        rect
    }

    fn text(&mut self, x: i32, y: i32, text: &str, class: &'static str) {
        self.texts.push(Text {
            x,
            y,
            text: text.into(),
            class,
            href: None,
        });
    }

    fn link(&mut self, x: i32, y: i32, text: &str, class: &'static str, href: Option<String>) {
        self.text(x, y, text, class);
        if let Some(last) = self.texts.last_mut() {
            last.href = href;
        }
    }

    fn line(&mut self, points: &[Point], style: Style, arrow: bool, title: Option<String>) {
        let d = match arrow {
            true => rounded(points),
            false => route::open(points),
        };
        self.routes.push(points.to_vec());
        self.lines.push(Line {
            from: points[0],
            to: points[points.len() - 1],
            d,
            style,
            title,
        });
    }

    fn dot(&mut self, x: i32, y: i32, gate: &Gate) {
        self.dots.push(Dot {
            x,
            y,
            gate: gate.clone(),
        });
    }

    /// Whether `line` runs off the drawing's right edge, open-ended, as
    /// "any address" does.
    pub fn open(line: &Line) -> bool {
        line.to.x >= WIDTH - 20
    }

    /// The classes a line is drawn with.
    pub fn line_class(&self, line: &Line) -> String {
        match Self::open(line) {
            true => format!("{} ax-open", line.style.class()),
            false => line.style.class().to_string(),
        }
    }

    /// The bottom of everything drawn, so the drawing's height includes
    /// it.
    fn bottom(&self) -> i32 {
        let boxes = self.boxes.iter().map(|b| b.rect.bottom());
        let sockets = self.sockets.iter().map(|s| s.rect.bottom());
        let texts = self.texts.iter().map(|t| t.y + 8);
        boxes.chain(sockets).chain(texts).max().unwrap_or(0)
    }

    /// Whether every line that ends with an arrow ends on a box's or a
    /// socket's edge.  A line with no arrow, which joins a trunk or runs
    /// off open-ended, ends on its last point.
    #[cfg(test)]
    pub fn ends_on_edges(&self) -> bool {
        let rects: Vec<Rect> = self
            .boxes
            .iter()
            .map(|b| b.rect)
            .chain(self.sockets.iter().map(|s| s.rect))
            .collect();
        self.lines
            .iter()
            .filter(|line| !line.d.ends_with(&format!(" {} {}", line.to.x, line.to.y)))
            .all(|line| rects.iter().any(|r| r.on_edge(line.to.x, line.to.y)))
    }

    /// The places where a level part of one line crosses an upright
    /// part of another.  Lines that meet where one of them ends, as
    /// when a caller's line joins its trunk or two lines share a port,
    /// touch and do not cross.
    #[cfg(test)]
    pub fn crossings(&self) -> Vec<Point> {
        let parts = |route: &Vec<Point>| -> Vec<(Point, Point)> {
            route.windows(2).map(|pair| (pair[0], pair[1])).collect()
        };
        let between = |value: i32, a: i32, b: i32| a.min(b) < value && value < a.max(b);
        let mut found = Vec::new();
        for (index, one) in self.routes.iter().enumerate() {
            for other in &self.routes[index + 1..] {
                for (a, b) in parts(one) {
                    for (c, d) in parts(other) {
                        let (level, upright) = match (a.y == b.y, c.x == d.x) {
                            (true, true) => ((a, b), (c, d)),
                            _ if c.y == d.y && a.x == b.x => ((c, d), (a, b)),
                            _ => continue,
                        };
                        let x = upright.0.x;
                        let y = level.0.y;
                        if between(x, level.0.x, level.1.x) && between(y, upright.0.y, upright.1.y)
                        {
                            found.push(Point { x, y });
                        }
                    }
                }
            }
        }
        found
    }

    /// Whether no two boxes overlap.
    #[cfg(test)]
    pub fn boxes_apart(&self) -> bool {
        let rects: Vec<Rect> = self.boxes.iter().map(|b| b.rect).collect();
        rects.iter().enumerate().all(|(i, a)| {
            rects[i + 1..].iter().all(|b| {
                !(a.x < b.right() && b.x < a.right() && a.y < b.bottom() && b.y < a.bottom())
            })
        })
    }
}
