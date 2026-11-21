//! Where a body or a piece is in its part: two lengths across and two
//! down, as CSS places an absolutely positioned box. A piece placed
//! against one edge keeps its size and its distance from that edge, and
//! a piece placed against both edges stretches with the part.

use serde::Deserialize;
use serde_yaml::Value;

use super::length::Length;

/// An edge of a part. A list of edges, such as a shape's joins and
/// ports, is in the order that the edges are declared here, so a page
/// reads the left before the right.
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Edge {
    Left,
    Right,
    Top,
    Bottom,
}

impl Edge {
    pub fn word(self) -> &'static str {
        match self {
            Self::Top => "top",
            Self::Right => "right",
            Self::Bottom => "bottom",
            Self::Left => "left",
        }
    }

    /// The edge after a quarter turn that lays a shape on its side: its
    /// top becomes its left, and its left its bottom.
    pub fn turned(self) -> Self {
        match self {
            Self::Top => Self::Left,
            Self::Left => Self::Bottom,
            Self::Bottom => Self::Right,
            Self::Right => Self::Top,
        }
    }

    /// The index of the edge in a list written top, right, bottom,
    /// left, as CSS writes `padding` and `inset`.
    pub fn index(self) -> usize {
        match self {
            Self::Top => 0,
            Self::Right => 1,
            Self::Bottom => 2,
            Self::Left => 3,
        }
    }
}

/// Two of the three lengths on one axis: the distance from the start
/// edge, the distance from the end edge, and the size.
#[derive(Clone, Debug, PartialEq)]
pub enum Span {
    FromStart { start: Length, size: Length },
    FromEnd { end: Length, size: Length },
    Between { start: Length, end: Length },
}

/// A rectangle placed across, from the left edge to the right, and down,
/// from the top edge to the bottom.
#[derive(Clone, Debug, PartialEq)]
pub struct Place {
    pub across: Span,
    pub down: Span,
}

/// A place at the design size, in design units from the top-left corner
/// of the part.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub left: f64,
    pub right: f64,
    pub top: f64,
    pub bottom: f64,
}

/// A place as written, with each of its six fields optional.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    left: Option<Value>,
    right: Option<Value>,
    width: Option<Value>,
    top: Option<Value>,
    bottom: Option<Value>,
    height: Option<Value>,
}

impl Place {
    /// The whole part.
    pub fn whole() -> Self {
        let edges = || Span::Between {
            start: Length::units(0.0),
            end: Length::units(0.0),
        };
        Self {
            across: edges(),
            down: edges(),
        }
    }

    /// Reads a place that states two of `left`, `right`, and `width`,
    /// and two of `top`, `bottom`, and `height`.
    pub fn read(written: Written, what: &str) -> Result<Self, String> {
        let across = span(
            [written.left, written.right, written.width],
            ["left", "right", "width"],
            what,
        )?;
        let down = span(
            [written.top, written.bottom, written.height],
            ["top", "bottom", "height"],
            what,
        )?;
        Ok(Self { across, down })
    }

    /// The place at the design size of a part `width` by `height`
    /// units.
    pub fn rect(&self, width: f64, height: f64) -> Rect {
        let (left, right) = self.across.ends(width);
        let (top, bottom) = self.down.ends(height);
        Rect {
            left,
            right,
            top,
            bottom,
        }
    }

    /// The place after a quarter turn: what was measured down from the
    /// top is measured across from the left, and what was measured
    /// across from the left is measured up from the bottom.
    pub fn turned(&self) -> Self {
        Self {
            across: self.down.clone(),
            down: self.across.reversed(),
        }
    }

    /// The span on the axis that crosses `edge`, which a piece that
    /// keeps the text clear of it is measured on.
    pub fn crossing(&self, edge: Edge) -> &Span {
        match edge {
            Edge::Left | Edge::Right => &self.across,
            Edge::Top | Edge::Bottom => &self.down,
        }
    }
}

impl Span {
    /// The start and the end of the span on an axis `whole` units long.
    fn ends(&self, whole: f64) -> (f64, f64) {
        match self {
            Self::FromStart { start, size } => (start.at(whole), start.at(whole) + size.at(whole)),
            Self::FromEnd { end, size } => {
                let end = whole - end.at(whole);
                (end - size.at(whole), end)
            }
            Self::Between { start, end } => (start.at(whole), whole - end.at(whole)),
        }
    }

    /// The span measured from the other end of its axis.
    fn reversed(&self) -> Self {
        match self.clone() {
            Self::FromStart { start, size } => Self::FromEnd { end: start, size },
            Self::FromEnd { end, size } => Self::FromStart { start: end, size },
            Self::Between { start, end } => Self::Between {
                start: end,
                end: start,
            },
        }
    }

    /// The distance from the edge at `start` (or the far edge when
    /// false) and the size, when both are plain numbers of design
    /// units.
    pub fn offset_and_size(&self, start: bool) -> Option<(f64, f64)> {
        match (self, start) {
            (Self::FromStart { start, size }, true)
            | (Self::FromEnd { end: start, size }, false) => {
                Some((start.plain_units()?, size.plain_units()?))
            }
            _ => None,
        }
    }
}

fn span(written: [Option<Value>; 3], names: [&str; 3], what: &str) -> Result<Span, String> {
    let read = |value: &Option<Value>, name: &str| {
        let value = value.as_ref();
        value
            .map(|value| Length::read(value, &format!("{what}.{name}")))
            .transpose()
    };
    let [start, end, size] = [
        read(&written[0], names[0])?,
        read(&written[1], names[1])?,
        read(&written[2], names[2])?,
    ];
    match (start, end, size) {
        (Some(start), None, Some(size)) => Ok(Span::FromStart { start, size }),
        (None, Some(end), Some(size)) => Ok(Span::FromEnd { end, size }),
        (Some(start), Some(end), None) => Ok(Span::Between { start, end }),
        _ => Err(format!(
            "{what} states two of {}, {}, and {}, and only two",
            names[0], names[1], names[2]
        )),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn place(text: &str) -> Result<Place, String> {
        Place::read(serde_yaml::from_str(text).unwrap(), "at")
    }

    #[rstest]
    #[case::from_the_left_and_top("{left: 12, width: 18, top: 100%, height: 4}", (12.0, 30.0, 120.0, 124.0))]
    #[case::from_the_right_and_bottom("{right: 16, width: 6, bottom: 16, height: 6}", (338.0, 344.0, 98.0, 104.0))]
    #[case::between_two_edges("{left: 0, right: 0, bottom: 8, height: 1}", (0.0, 360.0, 111.0, 112.0))]
    #[case::from_the_middle("{left: 50% - 54, width: 108, top: 0, bottom: 0}", (126.0, 234.0, 0.0, 120.0))]
    fn places_a_piece_at_the_design_size(
        #[case] text: &str,
        #[case] (left, right, top, bottom): (f64, f64, f64, f64),
    ) {
        let rect = place(text).unwrap().rect(360.0, 120.0);
        assert_eq!(
            rect,
            Rect {
                left,
                right,
                top,
                bottom
            }
        );
    }

    #[rstest]
    #[case::one_across(
        "{left: 1, top: 0, height: 4}",
        "at states two of left, right, and width, and only two"
    )]
    #[case::three_down(
        "{left: 1, width: 2, top: 0, bottom: 0, height: 4}",
        "at states two of top, bottom, and height, and only two"
    )]
    #[case::a_bad_length("{left: 1px, width: 2, top: 0, height: 4}", "at.left is not a length")]
    fn refuses_a_place_that_is_not_two_lengths_on_each_axis(
        #[case] text: &str,
        #[case] error: &str,
    ) {
        let found = place(text).unwrap_err();
        assert!(found.starts_with(error), "{found}");
    }

    #[rstest]
    fn refuses_a_field_a_place_does_not_have() {
        let written: Result<Written, _> =
            serde_yaml::from_str("{left: 0, width: 1, top: 0, height: 1, z: 2}");
        assert!(written.is_err());
    }

    /// A box 300 by 100 with a piece 10 wide at 20 from its left and 30
    /// tall at 5 from its top lies, after a quarter turn, in a box 100
    /// by 300: 5 from the left, and 20 up from the bottom.
    #[rstest]
    fn turns_a_place_a_quarter_turn() {
        let turned = place("{left: 20, width: 10, top: 5, height: 30}")
            .unwrap()
            .turned();
        let rect = turned.rect(100.0, 300.0);
        assert_eq!(
            rect,
            Rect {
                left: 5.0,
                right: 35.0,
                top: 270.0,
                bottom: 280.0
            }
        );
    }

    #[rstest]
    #[case::top(Edge::Top, Edge::Left)]
    #[case::left(Edge::Left, Edge::Bottom)]
    #[case::bottom(Edge::Bottom, Edge::Right)]
    #[case::right(Edge::Right, Edge::Top)]
    fn turns_each_edge(#[case] edge: Edge, #[case] turned: Edge) {
        assert_eq!(edge.turned(), turned);
    }
}
