// The divider: one thin rule with a name at its left and a year at its
// right. A series page draws one before each season's first row of
// stills. It takes no focus, so a press crosses it as if it were not
// there.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{area, extent, label};
use crate::look;

/// The height a divider takes in the stack that holds it.
pub const HEIGHT: f32 = 78.0;

// The thickness of the rule under the two words.
const RULE: f32 = 2.0;

// The space between the words and the rule under them.
const LIFT: f32 = 14.0;

/// Draw one divider in this region: the name at the left, the year at
/// the right, and the rule under both.
pub fn draw(frame: &mut canvas::Frame<Renderer>, region: Rectangle, name: &str, year: &str) {
    let baseline = region.y + region.height - LIFT - RULE;
    frame.fill_text(label(
        name,
        Point::new(region.x, baseline),
        look::HEADING,
        look::text(),
        Alignment::Left,
        Vertical::Bottom,
        region.width,
    ));
    frame.fill_text(label(
        year,
        Point::new(region.x + region.width, baseline),
        look::HEADING,
        look::muted(),
        Alignment::Right,
        Vertical::Bottom,
        region.width,
    ));

    let rule = area(
        region.x,
        region.y + region.height - RULE,
        region.width,
        RULE,
    );
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}
