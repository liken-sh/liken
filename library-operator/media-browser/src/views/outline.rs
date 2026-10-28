// The focus outline as a layer of its own. The browser draws it over the
// screen, the strip, or the picker, whichever holds focus, so one stroke
// can travel from slot to slot and breathe at rest without the screen
// under it drawing again. The browser decides where the outline is and
// how bright it is; this layer only strokes it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Rectangle, Theme, mouse};

use super::stroke;

/// The outline one frame draws: the rectangle the stroke follows, which is
/// `views::marked` of a slot at rest, and the stroke's color.
pub struct Outline {
    pub around: Rectangle,
    pub color: Color,
}

impl canvas::Program<Infallible, Theme, Renderer> for Outline {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        stroke(&mut frame, self.around, self.color);
        vec![frame.into_geometry()]
    }
}
