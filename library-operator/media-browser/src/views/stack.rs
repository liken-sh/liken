// The stack a page is: a column of blocks with a gap between two of them,
// and the scroll that keeps the block focus is on inside the viewport. A
// movie's page and a series' page are both this stack, so one rule
// decides what a press brings into view on either.

use iced_winit::core::{Point, Rectangle};

/// Where the next block of a page starts. A block that drew nothing moves
/// it nowhere and takes no gap, so a missing line leaves no hole.
pub struct Stack {
    at: Point,
    gap: f32,
}

impl Stack {
    /// A stack that starts at this corner and leaves this much space
    /// between two blocks.
    pub fn new(at: Point, gap: f32) -> Self {
        Self { at, gap }
    }

    /// Where the next block starts.
    pub fn at(&self) -> Point {
        self.at
    }

    /// Move down by the height a block took.
    pub fn add(&mut self, taken: f32) {
        if taken > 0.0 {
            self.at.y += taken + self.gap;
        }
    }
}

/// How far a page has scrolled. `region` is the block focus is on, in the
/// stack's own space. `tail` is the height of what follows it and takes
/// no focus of its own, so a block a press can never reach is brought
/// into view by the block above it. The page stands at its top until the
/// focused block and its tail would leave the foot of the viewport, and
/// the focused block never leaves the head of it.
pub fn offset(region: Rectangle, tail: f32, content: f32, height: f32) -> f32 {
    let most = region.y.min((content - height).max(0.0));
    (region.y + region.height + tail - height)
        .max(0.0)
        .min(most.max(0.0))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn block(top: f32, height: f32) -> Rectangle {
        Rectangle {
            x: 0.0,
            y: top,
            width: 100.0,
            height,
        }
    }

    #[test]
    fn a_stack_moves_down_by_what_a_block_took_and_its_gap() {
        let mut stack = Stack::new(Point::new(10.0, 20.0), 16.0);
        stack.add(30.0);
        assert_eq!(stack.at(), Point::new(10.0, 66.0));
        stack.add(0.0);
        assert_eq!(stack.at(), Point::new(10.0, 66.0));
    }

    #[test]
    fn a_page_that_fits_stands_at_its_top() {
        assert_eq!(offset(block(100.0, 200.0), 0.0, 900.0, 1080.0), 0.0);
        assert_eq!(offset(block(100.0, 200.0), 400.0, 900.0, 1080.0), 0.0);
    }

    #[test]
    fn a_focused_block_at_the_foot_scrolls_the_page() {
        assert_eq!(offset(block(900.0, 200.0), 0.0, 2000.0, 1080.0), 20.0);
    }

    #[test]
    fn what_follows_the_focused_block_comes_into_view_with_it() {
        assert_eq!(offset(block(700.0, 200.0), 300.0, 2000.0, 1080.0), 120.0);
    }

    #[test]
    fn the_scroll_stops_at_the_foot_of_the_page() {
        assert_eq!(offset(block(1800.0, 200.0), 0.0, 2000.0, 1080.0), 920.0);
    }

    #[test]
    fn the_focused_block_never_leaves_the_head_of_the_viewport() {
        assert_eq!(offset(block(200.0, 900.0), 600.0, 4000.0, 1080.0), 200.0);
    }
}
