//! The lines between two boxes in the right column, such as the line
//! from a device to the radio it pairs through.  Each line leaves the
//! right edge of its source box, runs in a lane of its own to the right
//! of the column, and enters the right edge of its target box.  The
//! lines from the column to the spine leave from the boxes' left edges,
//! so a line in a lane crosses no box and none of those lines.

use super::{Anchor, Drawn, Label, Line, Link, Point};
use crate::map::section::layout::{Layout, Side};

/// How much wider the section view is drawn when it has such a line, to
/// make room for the lanes and their labels.
pub const OUTER: i32 = 136;
/// The distance from the column's right edge to the first lane, and
/// between two lanes.
const LANE: i32 = 12;
const LABEL_GAP: i32 = 6;

/// Draws each link whose ends are both in the right column, and skips
/// every other link.
pub fn route(layout: &Layout, links: &[&Link]) -> Drawn {
    let mut drawn = Drawn::default();
    let placed = links.iter().filter_map(|link| {
        let right =
            link.from.on_side() == Some(Side::Right) && link.to.on_side() == Some(Side::Right);
        let source = layout.rect(link.from).filter(|_| right)?;
        let target = layout.rect(link.to)?;
        Some((link, source, target))
    });
    for (lane, (link, source, target)) in placed.enumerate() {
        let edge = source.right().max(target.right());
        let x = edge + LANE * (1 + i32::try_from(lane).unwrap_or(0));
        let points = [
            Point {
                x: source.right(),
                y: source.middle(),
            },
            Point {
                x,
                y: source.middle(),
            },
            Point {
                x,
                y: target.middle(),
            },
            Point {
                x: target.right(),
                y: target.middle(),
            },
        ];
        drawn.labels.push(Label {
            x: x + LABEL_GAP,
            y: (source.middle() + target.middle()) / 2,
            text: link.label.clone(),
            anchor: Anchor::Start,
        });
        drawn.lines.push(Line {
            from: points[0],
            to: points[3],
            d: super::side::rounded(&points),
            style: link.style,
            title: link.title.clone(),
        });
    }
    drawn
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::map::section::layout::{At, Shape, layout};
    use crate::map::section::lines::Style;

    /// One row with two boxes in its right column and a link from the
    /// first to the second.
    fn tied() -> (Layout, Link) {
        let shape = Shape {
            right: vec![1, 1],
            ..Shape::default()
        };
        let link = Link {
            from: At::side(0, Side::Right, 0),
            to: At::side(0, Side::Right, 1),
            label: "pairs through".into(),
            style: Style::Solid,
            title: None,
        };
        (layout(&[shape]), link)
    }

    #[rstest]
    fn runs_past_the_column_and_back_into_the_target() {
        let (layout, link) = tied();
        let drawn = route(&layout, &[&link]);
        let (source, target) = (
            layout.rect(link.from).unwrap(),
            layout.rect(link.to).unwrap(),
        );
        let line = &drawn.lines[0];
        assert_eq!(
            (line.from, line.to, drawn.labels[0].x > source.right()),
            (
                Point {
                    x: source.right(),
                    y: source.middle()
                },
                Point {
                    x: target.right(),
                    y: target.middle()
                },
                true
            )
        );
    }
}
