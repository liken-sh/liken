// The ground a movie's page lays under its rows. The head of the page,
// from the logo to the status line, keeps the backdrop with the scrim
// alone, because the scrim holds its full shade across the text column
// and the head draws nothing to the right of it but the art. Every row
// under the head runs the whole width of the frame, so the ground starts
// at the top of the first of them and runs to the foot of the frame.
//
// The ground moves up with the rows as the page scrolls, and stops at
// `VIEWPORT_TOP`, the line a scrolled page shows its rows under. Over that
// line the frame shows the backdrop under the strip's own backing, as it
// does at rest.

use iced_winit::core::Rectangle;

use super::super::Movie;
use super::{Block, Blocks, VIEWPORT_TOP};
use crate::views::layers::Ground;

impl Movie {
    /// The ground under the page's rows at these bounds, at the scroll
    /// this second draws.
    pub fn ground(&self, bounds: Rectangle, at: f64) -> Ground {
        let (blocks, offset) = self.drawn(bounds, at);
        under(&blocks, offset)
    }
}

// The ground under the rows of these blocks at this scroll down, and none
// on a page with nothing under its head.
fn under(blocks: &Blocks, offset: f32) -> Ground {
    match rows(blocks).next() {
        Some(first) => Ground::Below((first.top - offset).max(VIEWPORT_TOP)),
        None => Ground::None,
    }
}

// Every block under the head that draws, in the page's order: the strips
// and stripes, the studios, and the files.
fn rows(blocks: &Blocks) -> impl Iterator<Item = Block> + '_ {
    blocks
        .strip
        .into_iter()
        .chain(blocks.franchises.iter().copied())
        .chain(blocks.stripes.iter().copied())
        .chain(blocks.like)
        .chain(blocks.more)
        .chain([blocks.foot, blocks.files])
        .filter(|block| block.height > 0.0)
}

#[cfg(test)]
mod tests {
    use super::super::tests::{FRAME, SHORT, crowded};
    use super::super::{Focus, shown};
    use super::*;

    // Every row under the head draws inside the ground at its full
    // strength, at rest and at the scroll each focus rests at, so no
    // caption at the right end of a row draws on the bare backdrop.
    #[test]
    fn every_row_draws_inside_the_ground_at_any_focus_and_height() {
        let foci = [
            Focus::Buttons(0),
            Focus::Strip(0),
            Focus::Stripe(0, 0),
            Focus::Stripe(1, 0),
            Focus::Studio(0),
        ];
        for height in [FRAME.height, SHORT] {
            let bounds = Rectangle { height, ..FRAME };
            for focus in foci {
                let movie = crowded(focus);
                let blocks = Blocks::of(&movie, bounds);
                let offset = blocks.scroll(&movie, height);
                let ground = under(&blocks, offset).of(bounds).expect("a ground");
                let top = shown(bounds, offset).y;
                for block in rows(&blocks) {
                    let drawn = (block.top - offset).max(top);
                    assert!(
                        drawn >= ground.y,
                        "{focus:?} at {height}: {block:?} draws at {drawn}, over {ground:?}"
                    );
                }
            }
        }
    }

    // At rest the head keeps the backdrop: the ground at its full
    // strength starts under the status line.
    #[test]
    fn the_head_at_rest_stays_off_the_ground() {
        let movie = crowded(Focus::Buttons(0));
        let blocks = Blocks::of(&movie, FRAME);
        let ground = under(&blocks, 0.0).of(FRAME).expect("a ground");
        assert!(
            ground.y >= blocks.buttons.bottom(),
            "{ground:?} over {:?}",
            blocks.buttons
        );
    }

    // A scrolled page keeps the ground under the line the rows show
    // under, so the band under the strip shows the backdrop.
    #[test]
    fn the_ground_stops_at_the_line_under_the_strip() {
        let movie = crowded(Focus::Studio(0));
        let blocks = Blocks::of(&movie, FRAME);
        let offset = blocks.scroll(&movie, FRAME.height);
        assert_eq!(under(&blocks, offset), Ground::Below(VIEWPORT_TOP));
    }
}
