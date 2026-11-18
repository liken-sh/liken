//! The section view's lines, as SVG paths computed from the same layout
//! as its boxes. A spine line runs down from the middle of one box's
//! bottom edge to the middle of another box's top edge. It is straight
//! when the two boxes share a column. Otherwise it runs down, across
//! at the middle of the gap, and down again, so the lines from one box
//! to several share a trunk and are drawn as a fan. A line to a side box
//! runs across instead, on the route that `side` computes.

mod outer;
mod side;

pub use outer::OUTER;
pub use side::rounded;

use std::collections::HashMap;

use super::layout::{At, Layout, Rect};

/// How far the arrowhead's tip extends past the end of its path. Each
/// path stops this far short of its box so that the tip touches the
/// box's edge. The SVG marker's tip is 2 marker units past its reference
/// point, and the marker scales with the lines' stroke width of 2.
pub const ARROW: i32 = 4;
/// The radius of a line's corners.
const CORNER: i32 = 8;
/// A spine line turns at the middle of the gap between its ends, but
/// never more than this far below its source. So when rows of side boxes
/// lie between its ends, as with the layers of a workload's callers
/// between the entry and the workload, the line turns just below its
/// source and stays clear of the side lines' lanes.
const TURN: i32 = 48;
/// How far to the right of its line a spine label is drawn.
const LABEL_OFFSET: i32 = 10;

/// How a line is drawn, as the section view's legend explains: solid for
/// structure, dashed for callers, and dotted for an edge that transect
/// guessed.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Style {
    #[default]
    Solid,
    Dashed,
    Dotted,
}

impl Style {
    pub fn class(self) -> &'static str {
        match self {
            Self::Solid => "section-path",
            Self::Dashed => "section-path section-path-dashed",
            Self::Dotted => "section-path section-path-dotted",
        }
    }
}

/// Which end of a label its x coordinate is. A spine line's label
/// starts beside the line. A side line's label starts or ends at its
/// side box.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Anchor {
    Start,
    End,
}

impl Anchor {
    pub fn class(self) -> &'static str {
        match self {
            Self::Start => "section-label",
            Self::End => "section-label section-label-end",
        }
    }
}

/// One line to draw, by the boxes at its ends.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Link {
    pub from: At,
    pub to: At,
    pub label: String,
    pub style: Style,
    /// Where an edge came from when the API did not report it. The
    /// line shows this on hover.
    pub title: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Point {
    pub x: i32,
    pub y: i32,
}

#[derive(Debug, PartialEq, Eq)]
pub struct Line {
    /// The middle of the source's bottom edge.
    pub from: Point,
    /// The middle of the target's top edge, where the arrow's tip is.
    pub to: Point,
    pub d: String,
    pub style: Style,
    pub title: Option<String>,
}

#[derive(Debug, PartialEq, Eq)]
pub struct Label {
    pub x: i32,
    pub y: i32,
    pub text: String,
    /// Which end of the label `x` is.
    pub anchor: Anchor,
}

#[derive(Debug, Default, PartialEq, Eq)]
pub struct Drawn {
    pub lines: Vec<Line>,
    pub labels: Vec<Label>,
}

/// The paths of `links` over `layout`, and their labels. A link to a box
/// that the layout lacks is left out. When lines from one box share a
/// label, it is drawn once on their trunk. When lines into one box share
/// a label, it is drawn once on the drop into that box.
pub fn draw(layout: &Layout, links: &[Link]) -> Drawn {
    let (ties, rest): (Vec<&Link>, Vec<&Link>) = links.iter().partition(|link| is_tie(link));
    let (sides, spine): (Vec<&Link>, Vec<&Link>) = rest
        .into_iter()
        .partition(|link| link.from.on_side().is_some() || link.to.on_side().is_some());
    let mut drawn = spine_lines(layout, &spine);
    for routed in [side::route(layout, &sides), outer::route(layout, &ties)] {
        drawn.lines.extend(routed.lines);
        drawn.labels.extend(routed.labels);
    }
    drawn
}

/// Whether `link` joins two side boxes.
pub fn is_tie(link: &Link) -> bool {
    link.from.on_side().is_some() && link.to.on_side().is_some()
}

fn spine_lines(layout: &Layout, links: &[&Link]) -> Drawn {
    let placed: Vec<(&Link, Rect, Rect)> = links
        .iter()
        .copied()
        .filter_map(|link| Some((link, layout.rect(link.from)?, layout.rect(link.to)?)))
        .collect();
    let mut out: HashMap<At, Vec<&str>> = HashMap::new();
    let mut into: HashMap<At, Vec<&str>> = HashMap::new();
    for (link, _, _) in &placed {
        out.entry(link.from).or_default().push(&link.label);
        into.entry(link.to).or_default().push(&link.label);
    }
    let shared = |labels: Option<&Vec<&str>>| {
        labels.is_some_and(|labels| labels.len() > 1 && labels.iter().all(|l| *l == labels[0]))
    };
    let mut drawn = Drawn::default();
    for (link, source, target) in placed {
        let from = Point {
            x: source.center(),
            y: source.bottom(),
        };
        let to = Point {
            x: target.center(),
            y: target.y,
        };
        let middle = from.y + ((to.y - from.y) / 2).min(TURN);
        let (x, y) = if shared(out.get(&link.from)) {
            (from.x, (from.y + middle) / 2)
        } else if shared(into.get(&link.to)) || from.x != to.x {
            (to.x, (middle + to.y) / 2)
        } else {
            (from.x, middle)
        };
        let label = Label {
            x: x + LABEL_OFFSET,
            y,
            text: link.label.clone(),
            anchor: Anchor::Start,
        };
        if !drawn.labels.contains(&label) {
            drawn.labels.push(label);
        }
        drawn.lines.push(Line {
            d: path(from, to, middle),
            from,
            to,
            style: link.style,
            title: link.title.clone(),
        });
    }
    drawn
}

/// A path that runs straight down when the ends share a column.
/// Otherwise it runs down to `middle`, across with rounded corners, and
/// down to the end.
fn path(from: Point, to: Point, middle: i32) -> String {
    let end = to.y - ARROW;
    let dx = to.x - from.x;
    if dx == 0 {
        return format!("M {} {} V {end}", from.x, from.y);
    }
    let r = CORNER.min(dx.abs() / 2).min(middle - from.y).max(0);
    let s = dx.signum();
    format!(
        "M {} {} V {} Q {} {middle} {} {middle} H {} Q {} {middle} {} {} V {end}",
        from.x,
        from.y,
        middle - r,
        from.x,
        from.x + s * r,
        to.x - s * r,
        to.x,
        to.x,
        middle + r,
    )
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::map::section::layout::{Shape, layout};

    fn link(from: At, to: At, label: &str) -> Link {
        Link {
            from,
            to,
            label: label.into(),
            style: Style::Solid,
            title: None,
        }
    }

    /// One box per row down a spine of `rows` rows, each linked to the
    /// next.
    fn spine(rows: usize) -> (Layout, Vec<Link>) {
        let shapes = vec![Shape::tiers(vec![vec![2]]); rows];
        let links = (1..rows)
            .map(|row| link(At::new(row - 1, 0, 0), At::new(row, 0, 0), "selects"))
            .collect();
        (layout(&shapes), links)
    }

    /// One box above `count` boxes, linked to each, then each linked to
    /// one box below.
    fn fan(count: usize) -> (Layout, Vec<Link>) {
        let shapes = [vec![2], vec![2; count], vec![2]].map(|tier| Shape::tiers(vec![tier]));
        let out = (0..count).map(|i| link(At::new(0, 0, 0), At::new(1, 0, i), "mounted on"));
        let back = (0..count).map(|i| link(At::new(1, 0, i), At::new(2, 0, 0), "NFS"));
        (layout(&shapes), out.chain(back).collect())
    }

    /// Whether each line starts on its source's bottom edge and ends on
    /// its target's top edge, with its path running between them.
    fn ends_on_edges(layout: &Layout, links: &[Link]) -> bool {
        let drawn = draw(layout, links);
        drawn.lines.len() == links.len()
            && links.iter().zip(&drawn.lines).all(|(link, line)| {
                let source = layout.rect(link.from).unwrap();
                let target = layout.rect(link.to).unwrap();
                let on_bottom = line.from.y == source.bottom()
                    && (source.x..=source.x + source.width).contains(&line.from.x);
                let on_top = line.to.y == target.y
                    && (target.x..=target.x + target.width).contains(&line.to.x);
                let starts = line
                    .d
                    .starts_with(&format!("M {} {} ", line.from.x, line.from.y));
                let ends = line.d.ends_with(&format!("V {}", line.to.y - ARROW));
                on_bottom && on_top && starts && ends
            })
    }

    #[rstest]
    #[case::one_pod(spine(5))]
    #[case::three_machines(fan(3))]
    #[case::four_machines(fan(4))]
    #[case::two_backends(fan(2))]
    fn every_line_runs_from_edge_to_edge(#[case] boxed: (Layout, Vec<Link>)) {
        let (layout, links) = boxed;
        assert!(ends_on_edges(&layout, &links));
    }

    #[rstest]
    fn a_spine_is_straight() {
        let (layout, links) = spine(2);
        let drawn = draw(&layout, &links);
        assert_eq!(drawn.lines[0].d, "M 496 107 V 199");
    }

    #[rstest]
    fn a_fan_turns_at_the_middle_of_the_gap() {
        let (layout, links) = fan(3);
        let drawn = draw(&layout, &links);
        assert_eq!(
            drawn.lines[0].d,
            "M 496 107 V 147 Q 496 155 488 155 H 288 Q 280 155 280 163 V 199"
        );
    }

    #[rstest]
    #[case::a_spine(spine(3), vec![("selects", 506, 155), ("selects", 506, 315)])]
    #[case::a_fan_labels_its_trunk_and_its_drop(
        fan(3),
        vec![("mounted on", 506, 131), ("NFS", 506, 339)]
    )]
    fn labels_each_line_or_fan_once(
        #[case] boxed: (Layout, Vec<Link>),
        #[case] expected: Vec<(&str, i32, i32)>,
    ) {
        let (layout, links) = boxed;
        let drawn = draw(&layout, &links);
        let labels: Vec<(&str, i32, i32)> = drawn
            .labels
            .iter()
            .map(|l| (l.text.as_str(), l.x, l.y))
            .collect();
        assert_eq!(labels, expected);
    }

    #[rstest]
    fn a_line_across_rows_turns_just_under_its_source() {
        let between = Shape {
            left: vec![2],
            ..Shape::default()
        };
        let shapes = [
            Shape::tiers(vec![vec![2, 2]]),
            between,
            Shape::tiers(vec![vec![2]]),
        ];
        let links = [link(At::new(0, 0, 0), At::new(2, 0, 0), "selects")];
        let drawn = draw(&layout(&shapes), &links);
        assert!(drawn.lines[0].d.contains(&format!(" {} ", 107 + TURN)));
    }

    #[rstest]
    fn leaves_out_a_link_to_a_missing_box() {
        let (layout, _) = spine(2);
        let links = [link(At::new(0, 0, 0), At::new(5, 0, 0), "selects")];
        assert_eq!(draw(&layout, &links), Drawn::default());
    }
}
