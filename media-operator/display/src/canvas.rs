//! The coordinate space that the display lays out in, and the drawing
//! modules that use it. The canvas is [`theme::CANVAS_HEIGHT`] units tall,
//! and its width follows the surface's aspect ratio.

use iced::Size;

use crate::theme;

pub mod brush;
pub mod raster;
pub mod scrim;
pub mod shade;
pub mod shape;

pub use brush::{
    Anchor, Brush, ELLIPSIS, Line, clip, forget_measurements, measure, measure_italic,
};
pub use scrim::{Picture, Scrims};

/// The mapping from canvas units to output pixels. Both axes use the same
/// `scale`, because the canvas width follows the surface's aspect ratio.
///
/// The fields are private, so a canvas comes only from
/// [`Canvas::for_output`] or `Default`, and its scale is never 0. A scale
/// of 0 would make `to_canvas` divide by 0.
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
    /// The canvas for the surface size that the compositor configured.
    /// The width follows the surface's aspect ratio, so a canvas unit is
    /// square on the screen.
    ///
    /// A surface with no size yet gives the default canvas, because a
    /// canvas width of 0 would place every flush-right element off the
    /// screen.
    pub fn for_output(output: Size) -> Self {
        if output.width <= 0.0 || output.height <= 0.0 {
            return Self::default();
        }
        Self {
            width: (theme::CANVAS_HEIGHT * output.width / output.height + 0.5).floor(),
            scale: output.height / theme::CANVAS_HEIGHT,
        }
    }

    pub fn width(&self) -> f32 {
        self.width
    }

    pub fn height(&self) -> f32 {
        theme::CANVAS_HEIGHT
    }

    /// Output pixels per canvas unit.
    pub fn scale(&self) -> f32 {
        self.scale
    }

    /// A canvas length in whole output pixels, rounded half up. `snap`
    /// rounds through this too, so a snapped position and a length in
    /// pixels round the same way.
    pub fn to_pixels(&self, value: f32) -> f32 {
        (value * self.scale + 0.5).floor()
    }

    pub fn to_canvas(&self, pixels: f32) -> f32 {
        pixels / self.scale
    }

    /// A canvas value rounded to the output pixel grid, so an element at
    /// almost the same position in two frames is drawn on the same pixel.
    pub fn snap(&self, value: f32) -> f32 {
        self.to_canvas(self.to_pixels(value))
    }

    /// The x position where a flush-right element ends, inside the right
    /// margin.
    pub fn right(&self) -> f32 {
        self.width - theme::MARGIN_X
    }

    pub fn centre_x(&self) -> f32 {
        self.width / 2.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

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
    fn a_snapped_value_is_a_whole_number_of_output_pixels() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        assert_eq!(canvas.snap(270.6), 271.0);
        assert_eq!(canvas.snap(763.2), 763.0);

        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        assert_eq!(half.snap(270.6), 270.0);
        assert_eq!(half.snap(271.0), 271.5);
        assert_eq!((half.snap(271.0) * half.scale()).fract(), 0.0);
    }
}
