//! Estimates the heights of the boxes in a known kind's diagram.  The
//! server draws each line to the edge of a box, so it sets the box's
//! height before the browser lays out the text.  A box that the browser
//! sized could grow past the end of its line, and a box of fixed height
//! would clip its last lines.  The estimates count wrapped lines of the
//! monospace font and round up, so a box has room to spare.

use super::{Chip, End};

/// How many characters of the 12px monospace font in `known.css` fit
/// on one line inside a box's padding and border.
const LINE_CHARS: usize = 34;

/// The height of one line of text, and the gap between a box's items.
const LINE: i32 = 19;
const GAP: i32 = 4;

/// A box's padding and border, top and bottom together.
const FRAME: i32 = 2 * 12 + 2 * 2;

/// The heights of the object's name, which is in larger type, and of
/// one chip.
const NAME: i32 = 26;
const CHIP: i32 = 26;
const CHIP_GAP: i32 = 6;

/// The lines `text` wraps to across a box.
fn lines(text: &str) -> i32 {
    let chars = text.chars().count().max(1);
    chars.div_ceil(LINE_CHARS) as i32
}

/// The height of a stack of items of the heights in `items`, with the
/// gaps between them.
fn stacked(items: impl IntoIterator<Item = i32>) -> i32 {
    let heights: Vec<i32> = items.into_iter().collect();
    let gaps = GAP * (heights.len() as i32 - 1).max(0);
    heights.iter().sum::<i32>() + gaps
}

/// The height a relation's box needs for its label, each value with
/// the note under it, and the field the values were read from.  A box
/// with no values still shows one line, "names nothing now".
pub fn relation(label: &str, ends: &[End], source: &str) -> i32 {
    let mut items = vec![lines(label) * LINE];
    for end in ends {
        items.push(lines(&end.reference.text) * LINE);
        items.extend(end.under.as_deref().map(|under| lines(under) * LINE));
    }
    if ends.is_empty() {
        items.push(LINE);
    }
    items.push(lines(source) * LINE);
    FRAME + stacked(items)
}

/// The height the object's box needs for its kind, its name, the calm
/// reason in `resting` when there is one, and one line for each chip.
pub fn center(kind: &str, resting: Option<&str>, chips: &[Chip]) -> i32 {
    let chips = CHIP * chips.len() as i32 + CHIP_GAP * (chips.len() as i32 - 1).max(0);
    let resting = resting.map_or(0, |text| lines(text) * LINE);
    let items = [lines(kind) * LINE, NAME, resting, chips];
    FRAME + stacked(items.into_iter().filter(|height| *height > 0))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::map::known::chips::Status;
    use transect_graph::links::Reference;

    fn end(text: &str, under: Option<&str>) -> End {
        End {
            reference: Reference {
                text: text.into(),
                href: None,
            },
            under: under.map(Into::into),
            reaches: Vec::new(),
        }
    }

    fn chip(text: &str) -> Chip {
        Chip {
            text: text.into(),
            title: None,
            status: Status::True,
            level: None,
        }
    }

    #[rstest]
    #[case::one_short_end(vec![end("Remote hall", None)], FRAME + 3 * LINE + 2 * GAP)]
    #[case::nothing_named(vec![], FRAME + 3 * LINE + 2 * GAP)]
    #[case::an_end_that_reaches_more(
        vec![end("Node alpha", Some("reaches machine alpha"))],
        FRAME + 4 * LINE + 3 * GAP
    )]
    #[case::a_name_that_wraps(
        vec![end("Sink a-very-long-sink-name-that-runs-past-one-line", None)],
        FRAME + 4 * LINE + 2 * GAP
    )]
    fn sizes_a_relation_by_its_lines(#[case] ends: Vec<End>, #[case] height: i32) {
        assert_eq!(relation("shows on", &ends, "from status.screen"), height);
    }

    #[rstest]
    #[case::no_conditions(vec![], FRAME + LINE + NAME + GAP)]
    #[case::two_conditions(
        vec![chip("Ready"), chip("Power applied")],
        FRAME + LINE + NAME + 2 * CHIP + CHIP_GAP + 2 * GAP
    )]
    fn sizes_the_object_by_its_chips(#[case] chips: Vec<Chip>, #[case] height: i32) {
        assert_eq!(center("Player", None, &chips), height);
    }

    #[rstest]
    fn makes_room_for_the_calm_reason() {
        let resting = center("Player", Some("Not reserved"), &[]);
        assert_eq!(resting, center("Player", None, &[]) + LINE + GAP);
    }
}
