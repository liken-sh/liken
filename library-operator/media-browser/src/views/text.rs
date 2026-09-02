// The text primitive: one line, or a block cut to a number of lines with
// a fade over the cut. Both answer the height they took, so a page stacks
// its blocks.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point};

use super::{area, extent, label};
use crate::look;

/// The height of one line as a share of its size. Every block of text on
/// a page is measured in it.
pub const LEADING: f32 = 1.32;

// The width of an average glyph as a share of its size. It decides
// whether a block is longer than its cap.
const ADVANCE: f32 = 0.5;

/// How many lines this content takes at this size and width. The count
/// is an estimate from the number of characters, because the shaper runs
/// on the draw path and the page decides its geometry before it draws.
pub fn lines(content: &str, size: f32, width: f32) -> usize {
    if content.is_empty() {
        return 0;
    }
    let per_line = (width / (size * ADVANCE)).max(1.0);
    (content.chars().count() as f32 / per_line).ceil() as usize
}

/// The height a number of lines takes at this size.
pub fn height(lines: usize, size: f32) -> f32 {
    lines as f32 * size * LEADING
}

/// One line of text with its left edge at `at`, wrapped where the
/// content is longer than the width. The answer is the height it took,
/// and zero for a line the item does not carry, so the caller stacks the
/// next line under it.
pub fn line(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    at: Point,
    size: f32,
    color: Color,
    width: f32,
) -> f32 {
    if content.is_empty() {
        return 0.0;
    }
    frame.fill_text(label(
        content,
        at,
        size,
        color,
        Alignment::Left,
        Vertical::Top,
        width,
    ));
    height(lines(content, size, width), size)
}

/// A block of text cut to `cap` lines, with a fade over the last line
/// where the content is longer than the cut. The answer is the height the
/// block took, so the caller stacks the next block under it.
pub fn block(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    at: Point,
    size: f32,
    color: Color,
    width: f32,
    cap: usize,
) -> f32 {
    let taken = lines(content, size, width);
    if taken == 0 {
        return 0.0;
    }
    let leading = size * LEADING;
    let height = height(taken.min(cap), size);
    let block = area(at.x, at.y, width, height);

    // The clip cuts the block at its last line, so a plot of any length
    // draws in the space the page gave it and never over the row below.
    frame.with_clip(block, |frame| {
        frame.fill_text(label(
            content,
            at,
            size,
            color,
            Alignment::Left,
            Vertical::Top,
            width,
        ));
    });

    if taken > cap {
        let fade = area(at.x, at.y + height - leading, width, leading);
        frame.fill_rectangle(
            fade.position(),
            extent(fade),
            canvas::gradient::Linear::new(
                Point::new(fade.x, fade.y),
                Point::new(fade.x, fade.y + fade.height),
            )
            .add_stop(0.0, look::CLEAR)
            .add_stop(1.0, look::BACKGROUND),
        );
    }

    height
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_short_block_is_one_line() {
        assert_eq!(lines("A short plot.", 28.0, 900.0), 1);
    }

    #[test]
    fn a_line_the_item_does_not_carry_takes_no_height() {
        assert_eq!(lines("", 28.0, 900.0), 0);
        assert_eq!(height(0, 28.0), 0.0);
    }

    #[test]
    fn a_long_block_runs_past_four_lines() {
        let plot = "word ".repeat(80);
        assert!(lines(&plot, 28.0, 900.0) > 4);
    }

    #[test]
    fn a_narrow_block_holds_fewer_characters() {
        let plot = "word ".repeat(20);
        assert!(lines(&plot, 28.0, 120.0) > lines(&plot, 28.0, 1800.0));
    }

    #[test]
    fn a_line_is_as_tall_as_its_size_and_its_leading() {
        assert_eq!(height(2, 30.0), 2.0 * 30.0 * LEADING);
    }
}
