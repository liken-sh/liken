// The divider: one thin rule with a heading at its left. A series page
// draws one before each season's first row of stills. It takes no focus, so
// a press crosses it as if it were not there.

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

/// The space between the words and the rule under them, and the space a
/// sub-heading keeps over the foot of its own box.
pub const LIFT: f32 = 14.0;

/// Draw one divider in this region: the heading at the left, and the rule
/// under it.
pub fn draw(frame: &mut canvas::Frame<Renderer>, region: Rectangle, name: &str) {
    two(frame, region, name, name);
}

/// Draw one divider whose heading reads in two runs: `heading` whole in
/// the muted ink, and `bright`, its first run, over it in the text ink,
/// so a name reads first and what follows it second. The whole heading
/// draws muted and the first run draws over it, the way a strip's heading
/// does, so the shaper places both runs and no estimate of the first
/// run's width stands between them. A heading that is all one run passes
/// itself as both.
pub fn two(frame: &mut canvas::Frame<Renderer>, region: Rectangle, heading: &str, bright: &str) {
    let baseline = region.y + region.height - LIFT - RULE;
    for (content, color) in [(heading, look::muted()), (bright, look::text())] {
        frame.fill_text(label(
            content,
            Point::new(region.x, baseline),
            look::HEADING,
            color,
            Alignment::Left,
            Vertical::Bottom,
            region.width,
        ));
    }

    let rule = area(
        region.x,
        region.y + region.height - RULE,
        region.width,
        RULE,
    );
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}
