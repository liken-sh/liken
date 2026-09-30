//! The layout space and the one shape drawn in it so far. The canvas is
//! 1080 rows tall and as wide as the surface's own ratio makes it, and every
//! value that moves with the position snaps to a whole output pixel.

use iced::Size;

use crate::theme;

pub mod brush;
pub mod raster;
pub mod scrim;
pub mod shape;

pub use brush::{Anchor, Brush, ELLIPSIS, Line, clip, forget_measurements, measure};
pub use scrim::{Picture, Scrims};

/// How the layout space maps to the real surface. `scale` maps a canvas
/// length to output pixels, and the two axes share it because the width
/// follows the surface's own ratio.
///
/// The fields are private and [`Canvas::for_output`] is the one constructor,
/// because a scale of nothing divides through every conversion below and
/// turns every position it touches into NaN.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Canvas {
    width: f32,
    scale: f32,
}

impl Default for Canvas {
    fn default() -> Self {
        Self {
            width: theme::CANVAS_WIDTH,
            scale: 1.0,
        }
    }
}

impl Canvas {
    /// The canvas the surface the compositor configured calls for. The width
    /// follows the real surface's own ratio, so a canvas pixel is square. A
    /// fixed 1920 canvas on a 21:9 screen stretched every vector drawing by a
    /// third and pulled every margin inside where it belonged. A 16:9 surface
    /// gives 1920, the width this space always held, so a 16:9 screen draws
    /// every number it drew before.
    ///
    /// A surface with no size yet gives the default canvas, because a width
    /// of nothing would place every flush-right element off the screen.
    pub fn for_output(output: Size) -> Self {
        if output.width <= 0.0 || output.height <= 0.0 {
            return Self::default();
        }
        Self {
            width: (theme::CANVAS_HEIGHT * output.width / output.height + 0.5).floor(),
            scale: output.height / theme::CANVAS_HEIGHT,
        }
    }

    /// The canvas the surface gives: as wide as its ratio makes it, and always
    /// [`theme::CANVAS_HEIGHT`] rows tall.
    pub fn width(&self) -> f32 {
        self.width
    }

    pub fn height(&self) -> f32 {
        theme::CANVAS_HEIGHT
    }

    /// How many output pixels one canvas length covers.
    pub fn scale(&self) -> f32 {
        self.scale
    }

    /// One canvas length in whole output pixels. Every request, every
    /// placement, and every snap rounds one this way, so the two halves of a
    /// position never disagree by a pixel.
    pub fn to_pixels(&self, value: f32) -> f32 {
        (value * self.scale + 0.5).floor()
    }

    /// One length in output pixels as the canvas length it covers.
    pub fn to_canvas(&self, pixels: f32) -> f32 {
        pixels / self.scale
    }

    /// One canvas value on the output pixel grid, so what the display draws
    /// stands still between two frames that land on the same pixel.
    pub fn snap(&self, value: f32) -> f32 {
        self.to_canvas(self.to_pixels(value))
    }

    /// The column every flush-right element ends on.
    pub fn right(&self) -> f32 {
        self.width - theme::MARGIN_X
    }

    /// The middle of the screen, which a centred element measures from.
    pub fn centre_x(&self) -> f32 {
        self.width / 2.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A 16:9 surface gives exactly 1920, and a 21:9 surface gives 2560 at
    /// 1080 rows.
    #[test]
    fn the_canvas_width_follows_the_surface_ratio() {
        assert_eq!(
            Canvas::for_output(Size::new(1920.0, 1080.0)).width(),
            1920.0
        );
        assert_eq!(
            Canvas::for_output(Size::new(3840.0, 2160.0)).width(),
            1920.0
        );
        assert_eq!(
            Canvas::for_output(Size::new(2560.0, 1080.0)).width(),
            2560.0
        );
        assert_eq!(Canvas::for_output(Size::new(1280.0, 720.0)).width(), 1920.0);
        assert_eq!(Canvas::for_output(Size::new(1024.0, 768.0)).width(), 1440.0);
    }

    #[test]
    fn the_scale_maps_a_canvas_length_to_output_pixels() {
        assert_eq!(Canvas::for_output(Size::new(1920.0, 1080.0)).scale(), 1.0);
        assert_eq!(Canvas::for_output(Size::new(3840.0, 2160.0)).scale(), 2.0);
        assert_eq!(
            Canvas::for_output(Size::new(1280.0, 720.0)).scale(),
            2.0 / 3.0
        );
    }

    #[test]
    fn a_surface_with_no_size_gives_the_canvas_the_display_starts_on() {
        assert_eq!(Canvas::for_output(Size::new(0.0, 0.0)), Canvas::default());
        assert_eq!(
            Canvas::for_output(Size::new(1920.0, 0.0)),
            Canvas::default()
        );
        assert_eq!(
            Canvas::for_output(Size::new(-1.0, 1080.0)),
            Canvas::default()
        );
        assert_eq!(Canvas::default().width(), 1920.0);
        assert_eq!(Canvas::default().scale(), 1.0);
    }

    #[test]
    fn a_snapped_value_lands_on_a_whole_output_pixel() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        assert_eq!(canvas.snap(270.6), 271.0);
        assert_eq!(canvas.snap(763.2), 763.0);

        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        assert_eq!(half.snap(270.6), 270.0);
        assert_eq!(half.snap(271.0), 271.5);
        assert_eq!((half.snap(271.0) * half.scale()).fract(), 0.0);
    }
}
