//! How tall the boxes of a known kind's diagram have to be to show
//! everything in them.  The server places the boxes and draws the lines
//! to their edges, so it sizes each box before the browser lays out its
//! text.  A box sized by the browser alone would grow past the end of
//! the line drawn to it, and a box of a fixed height clips its last
//! lines.  The sizes are estimates from the box's width and the map's
//! monospace, which wraps at a known number of characters.  The
//! estimates are generous, so a box gets room to spare rather than too
//! little.

use super::{Chip, End};

/// The characters of the map's 12px monospace that fit across a box's
/// inside, after its padding and rule.
const CHARS: usize = 34;

/// One line of text, and the gap between a box's items.
const LINE: i32 = 19;
const GAP: i32 = 4;

/// A box's padding and rule, top and bottom together.
const FRAME: i32 = 2 * 12 + 2 * 2;

/// The object's name, in the larger type, and one condition's chip.
const NAME: i32 = 26;
const CHIP: i32 = 26;
const CHIP_GAP: i32 = 6;

/// The lines `text` wraps to across a box.
fn lines(text: &str) -> i32 {
    let chars = text.chars().count().max(1);
    chars.div_ceil(CHARS) as i32
}

/// The height of a stack of items of the heights in `items`, with the
/// gaps between them.
fn stacked(items: impl IntoIterator<Item = i32>) -> i32 {
    let heights: Vec<i32> = items.into_iter().collect();
    let gaps = GAP * (heights.len() as i32 - 1).max(0);
    heights.iter().sum::<i32>() + gaps
}

/// The height a relation's box needs for its label, each end with what
/// it reaches, and the field it was read from.  A box that names nothing
/// shows one line that states this.
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

/// The height the object's own box needs for its kind, its name, why
/// it is at rest when it is, and each condition's chip on a line of its
/// own.
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
    fn makes_room_for_why_it_rests() {
        let resting = center("Player", Some("Not reserved"), &[]);
        assert_eq!(resting, center("Player", None, &[]) + LINE + GAP);
    }
}
