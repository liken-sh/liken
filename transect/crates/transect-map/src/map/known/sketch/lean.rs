//! Which edge of its area a role's parts stand against. The parts of a
//! sketch gather toward the middle of the grid, so an area left of the
//! middle puts its parts at its right edge, next to the area beside it.
//! In a row of a speaker, a box two columns wide, and a handheld, the
//! speaker then stands next to the box and not across a wide gap from
//! it. The rule reads only the columns of the area, so it is the same
//! for every kind.

use std::ops::Range;

/// The edge of its area that a role's parts stand against.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Lean {
    /// The area is wholly right of the grid's vertical center line, so
    /// its parts stand at its left edge.
    Start,
    /// The area spans the center line or has an edge on it, so its
    /// parts stay centered.
    Center,
    /// The area is wholly left of the center line, so its parts stand
    /// at its right edge.
    End,
}

impl Lean {
    /// The lean of an area that spans `columns` of a grid that has
    /// `count` columns. The center line is at `count / 2`. The
    /// comparisons double each edge in place of halving `count`, so an
    /// odd count needs no fraction.
    pub fn of(columns: Range<usize>, count: usize) -> Self {
        if 2 * columns.end < count {
            Self::End
        } else if 2 * columns.start > count {
            Self::Start
        } else {
            Self::Center
        }
    }

    /// The class of the area. A centered area needs no class, because
    /// an area centers its parts unless a class says otherwise.
    pub fn area_class(self) -> &'static str {
        match self {
            Self::Start => "sketch-area sketch-lean-start",
            Self::Center => "sketch-area",
            Self::End => "sketch-area sketch-lean-end",
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::the_one_column_of_one(0..1, 1, Lean::Center)]
    #[case::the_left_of_two(0..1, 2, Lean::Center)]
    #[case::the_right_of_two(1..2, 2, Lean::Center)]
    #[case::the_left_of_three(0..1, 3, Lean::End)]
    #[case::the_middle_of_three(1..2, 3, Lean::Center)]
    #[case::the_right_of_three(2..3, 3, Lean::Start)]
    #[case::two_of_three_across_the_middle(0..2, 3, Lean::Center)]
    #[case::the_first_of_four(0..1, 4, Lean::End)]
    #[case::the_second_of_four_on_the_line(1..2, 4, Lean::Center)]
    #[case::the_middle_two_of_four(1..3, 4, Lean::Center)]
    #[case::the_third_of_four_on_the_line(2..3, 4, Lean::Center)]
    #[case::the_last_of_four(3..4, 4, Lean::Start)]
    #[case::the_left_half_of_four(0..2, 4, Lean::Center)]
    #[case::the_whole_row(0..4, 4, Lean::Center)]
    #[case::the_left_two_of_five(0..2, 5, Lean::End)]
    #[case::the_second_of_five(1..2, 5, Lean::End)]
    #[case::the_middle_of_five(2..3, 5, Lean::Center)]
    #[case::the_right_two_of_five(3..5, 5, Lean::Start)]
    fn gathers_the_parts_of_an_area_toward_the_middle(
        #[case] columns: Range<usize>,
        #[case] count: usize,
        #[case] lean: Lean,
    ) {
        assert_eq!(Lean::of(columns, count), lean);
    }

    #[rstest]
    #[case::start(Lean::Start, "sketch-area sketch-lean-start")]
    #[case::center(Lean::Center, "sketch-area")]
    #[case::end(Lean::End, "sketch-area sketch-lean-end")]
    fn writes_a_class_for_each_lean(#[case] lean: Lean, #[case] class: &str) {
        assert_eq!(lean.area_class(), class);
    }
}
