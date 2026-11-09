// Every line of text the browser draws goes through `ink`, because a
// clip does not hold text in. A canvas clip is a draft frame with clip
// bounds of its own, and a draft made inside another takes its own
// bounds and not the part they share with the outer one. The text
// helpers clip each line to its own band, so a caption that a page
// scrolled up past its clip still draws in full, over the strip's clock
// or a series' header. Art and fills are not clipped by the helpers,
// so the page's clip cuts them.
//
// A screen that scrolls rows up under a fixed part of the frame draws
// them `under` the line where the fixed part ends. While it does, `ink`
// draws no text whose line box reaches above that line. A caption cut
// in half by the line reads worse than no caption, so the whole line of
// text goes.

use std::cell::Cell;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::LineHeight;

thread_local! {
    // The line no text may reach above while a screen draws its scrolled
    // rows, and nothing while no screen does.
    static LINE: Cell<Option<f32>> = const { Cell::new(None) };
}

/// Draw `draw` with no text above `line`, in logical pixels from the top
/// of the frame. A screen that draws one scrolled part inside another
/// keeps the lower of the two lines.
pub fn under<R>(line: f32, draw: impl FnOnce() -> R) -> R {
    let outer = LINE.get();
    LINE.set(Some(outer.map_or(line, |outer| outer.max(line))));
    let drawn = draw();
    LINE.set(outer);
    drawn
}

/// Draw one text, unless its line box reaches above the line a scrolled
/// screen draws under.
pub fn ink(frame: &mut canvas::Frame<Renderer>, text: canvas::Text) {
    if shows(&text, LINE.get()) {
        frame.fill_text(text);
    }
}

// Whether a text whose line box is this far down lies wholly under the
// line, or draws while no line holds.
fn shows(text: &canvas::Text, line: Option<f32>) -> bool {
    line.is_none_or(|line| top(text) >= line)
}

// The top of a text's line box: the point it is drawn at, moved up by the
// part of its lines the vertical alignment puts over that point.
fn top(text: &canvas::Text) -> f32 {
    let lines = text.content.lines().count().max(1) as f32;
    let height = lines * line_height(text);
    match text.align_y {
        Vertical::Top => text.position.y,
        Vertical::Center => text.position.y - height / 2.0,
        Vertical::Bottom => text.position.y - height,
    }
}

fn line_height(text: &canvas::Text) -> f32 {
    match text.line_height {
        LineHeight::Absolute(pixels) => pixels.0,
        LineHeight::Relative(factor) => factor * text.size.0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::{label, text};
    use iced_winit::core::Point;
    use iced_winit::core::text::Alignment;

    const SIZE: f32 = 20.0;

    fn at(y: f32, align: Vertical, content: &str) -> canvas::Text {
        label(
            content,
            Point::new(100.0, y),
            SIZE,
            crate::look::text(),
            Alignment::Left,
            align,
            f32::INFINITY,
        )
    }

    #[test]
    fn a_text_draws_only_where_its_whole_line_box_lies_under_the_line() {
        let line = SIZE * text::LEADING;
        let cases = [
            (at(160.0, Vertical::Top, "Caption"), Some(154.0), true),
            (at(154.0, Vertical::Top, "Caption"), Some(154.0), true),
            (at(150.0, Vertical::Top, "Caption"), Some(154.0), false),
            (at(80.0, Vertical::Top, "Caption"), Some(154.0), false),
            (
                at(154.0 + line / 2.0, Vertical::Center, "Caption"),
                Some(154.0),
                true,
            ),
            (at(160.0, Vertical::Center, "Caption"), Some(154.0), false),
            (
                at(154.0 + line, Vertical::Bottom, "Caption"),
                Some(154.0),
                true,
            ),
            (at(170.0, Vertical::Bottom, "Caption"), Some(154.0), false),
            (
                at(154.0 + line, Vertical::Center, "One\nTwo"),
                Some(154.0),
                true,
            ),
            (
                at(154.0 + line / 2.0, Vertical::Center, "One\nTwo"),
                Some(154.0),
                false,
            ),
            (at(-40.0, Vertical::Top, "Caption"), None, true),
        ];
        for (text, line, shown) in cases {
            assert_eq!(
                shows(&text, line),
                shown,
                "{:?} under {line:?}",
                text.position
            );
        }
    }

    #[test]
    fn a_line_holds_only_while_its_screen_draws_and_the_lower_line_wins() {
        assert_eq!(LINE.get(), None);
        under(154.0, || {
            assert_eq!(LINE.get(), Some(154.0));
            under(120.0, || assert_eq!(LINE.get(), Some(154.0)));
            under(300.0, || assert_eq!(LINE.get(), Some(300.0)));
            assert_eq!(LINE.get(), Some(154.0));
        });
        assert_eq!(LINE.get(), None);
    }
}
