// The text primitive: one line, or a block cut to a number of lines. Both
// answer the height they took, so a page stacks its blocks.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight, Shaping};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle};

use super::{area, label};
use crate::look;

/// The height of one line as a share of its size. Every block of text on
/// a page is measured in it.
pub const LEADING: f32 = 1.32;

// The width of an average glyph as a share of its size. It decides
// whether a block is longer than its cap.
const ADVANCE: f32 = 0.5;

/// How many characters of this size a width holds. It is an estimate from
/// the average advance, because the shaper runs on the draw path and a
/// caller decides its geometry before it draws.
pub fn fits(size: f32, width: f32) -> usize {
    (width / (size * ADVANCE)).max(0.0) as usize
}

/// The width this content takes at this size, from the same average advance
/// the line count uses.
pub fn width(content: &str, size: f32) -> f32 {
    content.chars().count() as f32 * size * ADVANCE
}

/// The width this content draws at, from the shaper's own paragraph over
/// the display's font, so a line placed after it never drifts with the
/// glyphs the estimate cannot see. The shaper runs on the draw path
/// already, and one paragraph of a short line costs less than a frame.
/// The measure shapes with the brand's faces, loaded once on the first
/// call, so a test and the screen measure the same face.
pub fn measured(content: &str, size: f32) -> f32 {
    use iced_winit::core::text::{Paragraph, Text, Wrapping};
    static FACES: std::sync::Once = std::sync::Once::new();
    FACES.call_once(liken_iced::font::load);
    let paragraph = iced_wgpu::graphics::text::Paragraph::with_text(Text {
        content,
        bounds: iced_winit::core::Size::INFINITE,
        size: Pixels(size),
        line_height: LineHeight::Absolute(Pixels(size * LEADING)),
        font: Font::with_name(look::FONT),
        align_x: Alignment::Left,
        align_y: Vertical::Top,
        shaping: Shaping::Advanced,
        wrapping: Wrapping::None,
    });
    paragraph.min_width()
}

/// The content cut to what one line of this width holds at this size,
/// with an ellipsis where it was cut. A caption band is one line tall,
/// and a line the shaper wrapped would show the tops of its second line
/// inside the band's clip.
pub fn cut(content: &str, size: f32, width: f32) -> String {
    let room = fits(size, width);
    if content.chars().count() <= room {
        return content.to_string();
    }
    let kept: String = content.chars().take(room.saturating_sub(1)).collect();
    format!("{}\u{2026}", kept.trim_end())
}

/// The content cut to the widest prefix the shaper sets inside this
/// width, with the same ellipsis `cut` leaves. The estimate under `cut`
/// can call a caption short enough that the shaper sets wider than its
/// band, so a caption that has to fit exactly is cut here.
pub fn measured_cut(content: &str, size: f32, width: f32) -> String {
    if measured(content, size) <= width {
        return content.to_string();
    }
    let letters: Vec<char> = content.chars().collect();
    let (mut kept, mut over) = (0, letters.len());
    while kept < over {
        let middle = (kept + over).div_ceil(2);
        match measured(&ellipsed(&letters[..middle]), size) <= width {
            true => kept = middle,
            false => over = middle - 1,
        }
    }
    ellipsed(&letters[..kept])
}

// One ellipsis after the letters that were kept, the convention `cut`
// writes.
fn ellipsed(letters: &[char]) -> String {
    let kept: String = letters.iter().collect();
    format!("{}\u{2026}", kept.trim_end())
}

/// How many lines this content takes at this size and width. The count
/// is an estimate from the number of characters, because the shaper runs
/// on the draw path and the page decides its geometry before it draws.
pub fn lines(content: &str, size: f32, width: f32) -> usize {
    if content.is_empty() {
        return 0;
    }
    content.chars().count().div_ceil(fits(size, width).max(1))
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
    line_in(
        frame,
        content,
        at,
        (size, Font::with_name(look::FONT)),
        color,
        width,
    )
}

/// One line at a size, in a named face, so a tagline draws in the
/// family's italic while every other line keeps the roman one.
pub fn line_in(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    at: Point,
    (size, font): (f32, Font),
    color: Color,
    width: f32,
) -> f32 {
    if content.is_empty() {
        return 0.0;
    }
    let mut text = label(
        content,
        at,
        size,
        color,
        Alignment::Left,
        Vertical::Top,
        width,
    );
    text.font = font;
    frame.fill_text(text);
    height(lines(content, size, width), size)
}

/// One line centered in its band and clipped to it, so a long line never
/// runs off the screen or over what is beside it. The line is cut to the
/// band's width with an ellipsis, because a band is one line tall and a
/// wrapped line would show the tops of a second. A band with nothing in
/// it draws nothing.
pub fn centered(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    band: Rectangle,
    size: f32,
    color: Color,
) {
    shown(frame, &cut(content, size, band.width), band, size, color);
}

/// One line centered in its band and clipped to it, drawn as it stands.
/// The caller cut it to the band at the read, and a second cut by the
/// estimate would take letters the shaper set inside the band.
pub fn shown(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    band: Rectangle,
    size: f32,
    color: Color,
) {
    faced(
        frame,
        content,
        band,
        size,
        color,
        Font::with_name(look::FONT),
    );
}

/// One line centered in its band and clipped to it, in a named face, so
/// a caption's second line draws in the family's italic while every
/// other line keeps the roman one.
pub fn faced(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    band: Rectangle,
    size: f32,
    color: Color,
    font: Font,
) {
    if content.is_empty() {
        return;
    }
    frame.with_clip(band, |frame| {
        frame.fill_text(canvas::Text {
            font,
            ..label(
                content,
                Point::new(band.center_x(), band.y),
                size,
                color,
                Alignment::Center,
                Vertical::Top,
                f32::INFINITY,
            )
        });
    });
}

/// A block of text cut to `cap` lines. The answer is the height the block
/// took, so the caller stacks the next block under it.
pub fn block(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    at: Point,
    size: f32,
    color: Color,
    width: f32,
    cap: usize,
) -> f32 {
    block_in(
        frame,
        content,
        at,
        (size, Font::with_name(look::FONT)),
        color,
        width,
        cap,
    )
}

/// A block at a size, in a named face, so a tagline draws in the
/// family's italic while every other block keeps the roman one.
pub fn block_in(
    frame: &mut canvas::Frame<Renderer>,
    content: &str,
    at: Point,
    (size, font): (f32, Font),
    color: Color,
    width: f32,
    cap: usize,
) -> f32 {
    let taken = lines(content, size, width);
    if taken == 0 {
        return 0.0;
    }
    let height = height(taken.min(cap), size);
    let block = area(at.x, at.y, width, height);

    // The clip cuts the block at its last line, so a plot of any length
    // draws in the space the page gave it and never over the row below.
    frame.with_clip(block, |frame| {
        let mut text = label(
            content,
            at,
            size,
            color,
            Alignment::Left,
            Vertical::Top,
            width,
        );
        text.font = font;
        frame.fill_text(text);
    });

    height
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_caption_longer_than_its_band_ends_in_an_ellipsis() {
        let room = fits(16.0, 120.0);
        let long: String = "a".repeat(room + 5);
        let shown = cut(&long, 16.0, 120.0);
        assert_eq!(shown.chars().count(), room);
        assert!(shown.ends_with('\u{2026}'));
        assert_eq!(cut("short", 16.0, 120.0), "short");
    }

    #[test]
    fn a_caption_the_shaper_sets_wider_than_its_band_is_cut_to_fit_it() {
        let long = "W".repeat(40);
        let shown = measured_cut(&long, 18.0, 200.0);
        assert!(shown.ends_with('\u{2026}'));
        assert!(shown.chars().count() < long.chars().count());
        assert!(measured(&shown, 18.0) <= 200.0);
    }

    #[test]
    fn a_caption_the_shaper_sets_inside_its_band_is_cut_nowhere() {
        assert_eq!(
            measured_cut("Specimen 0001", 18.0, 2_000.0),
            "Specimen 0001"
        );
        assert_eq!(measured_cut("", 18.0, 200.0), "");
    }

    #[test]
    fn a_band_too_narrow_for_one_letter_holds_the_ellipsis_alone() {
        assert_eq!(measured_cut("Specimen 0001", 18.0, 0.0), "\u{2026}");
    }

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
    fn a_band_holds_the_characters_its_width_allows() {
        assert_eq!(fits(26.0, 320.0), 24);
        assert_eq!(fits(26.0, 640.0), 49);
        assert_eq!(fits(26.0, 0.0), 0);
    }

    #[test]
    fn a_width_and_a_count_of_characters_agree() {
        assert_eq!(width("abcd", 26.0), 4.0 * 26.0 * ADVANCE);
        assert_eq!(width("", 26.0), 0.0);
        assert_eq!(fits(26.0, width("abcd", 26.0)), 4);
    }

    #[test]
    fn a_line_is_as_tall_as_its_size_and_its_leading() {
        assert_eq!(height(2, 30.0), 2.0 * 30.0 * LEADING);
    }
}
