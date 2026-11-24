//! The dark gradients at the top and bottom of the screen, behind the header
//! and the scrubber, so their text is readable over any video frame.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use super::Canvas;
use crate::theme;

/// The screen edge where a scrim is darkest. It fades to clear away from
/// that edge.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Edge {
    Top,
    Bottom,
}

/// One scrim. Its opacity stays at the peak from the screen edge out to
/// `hold`, then falls to zero at `clear` along a single smoothstep curve.
/// The curve has no flat section inside the fade, because a flat section
/// shows as the edge of a band.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Scrim {
    edge: Edge,
    /// The distance from the screen edge, in canvas rows, up to which the
    /// opacity stays at its peak.
    hold: f32,
    /// The distance from the screen edge, in canvas rows, at which the
    /// opacity reaches zero.
    clear: f32,
}

impl Scrim {
    /// The scrim behind the header and the clock, at the top edge.
    pub fn top() -> Self {
        Self {
            edge: Edge::Top,
            hold: theme::SCRIM_TOP_HOLD,
            clear: theme::SCRIM_TOP_CLEAR,
        }
    }

    /// The scrim behind the scrubber and the control strip, at the bottom
    /// edge.
    pub fn bottom() -> Self {
        Self {
            edge: Edge::Bottom,
            hold: theme::SCRIM_BOTTOM_HOLD,
            clear: theme::SCRIM_BOTTOM_CLEAR,
        }
    }

    /// The scrim behind the cast row, which the display draws instead of the
    /// bottom scrim while the cast row shows. Its peak reaches above the
    /// row's heading, so the heading and the names are readable over a
    /// bright frame.
    pub fn cast() -> Self {
        Self {
            edge: Edge::Bottom,
            hold: theme::SCRIM_CAST_HOLD,
            clear: theme::SCRIM_CAST_CLEAR,
        }
    }

    /// The opacity of the scrim at one canvas row, from 0 to the peak,
    /// `alpha::SCRIM`.
    pub fn opacity_at(&self, row: f32) -> f32 {
        let from_edge = match self.edge {
            Edge::Top => row,
            Edge::Bottom => theme::CANVAS_HEIGHT - row,
        };
        let t = ((from_edge - self.hold) / (self.clear - self.hold)).clamp(0.0, 1.0);
        theme::alpha::SCRIM * (1.0 - t * t * (3.0 - 2.0 * t))
    }

    /// The scrim as an image one texel wide and one texel per output row,
    /// which the display stretches across the width. An image costs one
    /// texture read per pixel, while iced's gradient fill searches its color
    /// stops for every pixel.
    ///
    /// `density` is the window's scale factor, in output pixels per logical
    /// pixel. The image starts at the screen edge, so each texel lines up
    /// with one output row and holds the opacity at the middle of that row.
    /// Returns `None` when the scrim covers less than one output row.
    pub fn picture(&self, canvas: &Canvas, density: f32) -> Option<Picture> {
        let per_row = canvas.scale() * density;
        let rows = (self.clear * per_row).ceil();
        if !rows.is_finite() || rows < 1.0 {
            return None;
        }
        let height = rows / per_row;
        let top = match self.edge {
            Edge::Top => 0.0,
            Edge::Bottom => theme::CANVAS_HEIGHT - height,
        };
        let [red, green, blue] = [
            theme::color::SHADOW.r,
            theme::color::SHADOW.g,
            theme::color::SHADOW.b,
        ]
        .map(|channel| (channel * 255.0).round() as u8);
        let pixels: Vec<u8> = (0..rows as u32)
            .flat_map(|index| {
                let alpha = self.opacity_at(top + (index as f32 + 0.5) / per_row);
                [red, green, blue, (alpha * 255.0).round() as u8]
            })
            .collect();
        Some(Picture {
            handle: Handle::from_rgba(1, rows as u32, pixels),
            bounds: Rectangle::new(Point::new(0.0, top), Size::new(canvas.width(), height)),
        })
    }
}

/// The scrim images for one canvas and one scale factor. The fade level
/// changes between frames, but the brush applies it as the opacity of the
/// whole image, so the images are built again only when the canvas or the
/// scale factor changes.
#[derive(Debug, Clone, Default)]
pub struct Scrims {
    pub top: Option<Picture>,
    pub bottom: Option<Picture>,
    pub cast: Option<Picture>,
}

impl Scrims {
    pub fn for_canvas(canvas: &Canvas, density: f32) -> Self {
        Self {
            top: Scrim::top().picture(canvas, density),
            bottom: Scrim::bottom().picture(canvas, density),
            cast: Scrim::cast().picture(canvas, density),
        }
    }
}

/// One scrim image and the canvas rectangle it stretches over.
#[derive(Debug, Clone)]
pub struct Picture {
    pub handle: Handle,
    pub bounds: Rectangle,
}

impl Picture {
    /// The alpha byte of each row, top to bottom. Tests read the image this
    /// way because an image handle takes a new id on every call, so two
    /// handles of the same pixels differ.
    pub fn rows(&self) -> Vec<u8> {
        match &self.handle {
            Handle::Rgba { pixels, .. } => pixels.chunks(4).map(|pixel| pixel[3]).collect(),
            _ => Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn canvas() -> Canvas {
        Canvas::for_output(Size::new(1920.0, 1080.0))
    }

    /// Each scrim is at its peak from the screen edge to `hold` and is clear
    /// from `clear` on.
    #[test]
    fn a_scrim_is_at_its_peak_to_hold_and_clear_from_clear() {
        let peak = theme::alpha::SCRIM;
        let (top, bottom) = (Scrim::top(), Scrim::bottom());
        assert_eq!(top.opacity_at(0.0), peak);
        assert_eq!(top.opacity_at(theme::SCRIM_TOP_HOLD), peak);
        assert_eq!(top.opacity_at(theme::SCRIM_TOP_CLEAR), 0.0);
        assert_eq!(bottom.opacity_at(1080.0), peak);
        assert_eq!(bottom.opacity_at(1080.0 - theme::SCRIM_BOTTOM_HOLD), peak);
        assert_eq!(bottom.opacity_at(1080.0 - theme::SCRIM_BOTTOM_CLEAR), 0.0);
    }

    /// The cast scrim is at its peak at the row's heading, and is at least as
    /// opaque as the bottom scrim at every row.
    #[test]
    fn the_cast_scrim_is_at_its_peak_over_the_heading_and_covers_the_bottom() {
        let (cast, bottom) = (Scrim::cast(), Scrim::bottom());
        assert_eq!(cast.opacity_at(crate::cast::TOP), theme::alpha::SCRIM);
        assert!((0..1080).all(|row| cast.opacity_at(row as f32) >= bottom.opacity_at(row as f32)));
    }

    /// Across the fade, the step in opacity from one output row to the next
    /// grows to a maximum and then shrinks, with no flat section between.
    #[test]
    fn the_fade_steepens_once_and_eases_once() {
        let rows: Vec<i32> = Scrim::top()
            .picture(&canvas(), 1.0)
            .expect("the top scrim")
            .rows()
            .into_iter()
            .map(i32::from)
            .collect();
        let hold = theme::SCRIM_TOP_HOLD as usize;
        let steps: Vec<i32> = rows[hold..].windows(9).map(|w| w[0] - w[8]).collect();
        let steepest = steps
            .iter()
            .enumerate()
            .max_by_key(|(_, step)| **step)
            .map(|(at, _)| at)
            .expect("a fade");
        assert!(steps[..steepest].windows(2).all(|w| w[0] <= w[1] + 1));
        assert!(steps[steepest..].windows(2).all(|w| w[0] + 1 >= w[1]));
        assert!(steps[5..steps.len() - 5].iter().all(|step| *step > 0));
    }

    /// The image is one texel wide, in the shadow color, for each row.
    #[test]
    fn the_picture_is_one_shadow_texel_per_row() {
        let picture = Scrim::bottom()
            .picture(&canvas(), 1.0)
            .expect("the bottom scrim");
        let Handle::Rgba { width, pixels, .. } = &picture.handle else {
            panic!("a scrim is RGBA");
        };
        assert_eq!(*width, 1);
        assert!(pixels.chunks(4).all(|pixel| pixel[..3] == [0, 0, 0]));
    }

    /// The image covers the whole width from the screen edge with one row
    /// per output row, so a window at twice the scale has twice the rows over
    /// the same canvas rows.
    #[test]
    fn the_picture_covers_whole_output_rows_from_the_screen_edge() {
        let top = Scrim::top().picture(&canvas(), 1.0).expect("the top scrim");
        assert_eq!(top.bounds.y, 0.0);
        assert_eq!(top.bounds.width, 1920.0);
        assert_eq!(top.rows().len(), theme::SCRIM_TOP_CLEAR as usize);

        let bottom = Scrim::bottom()
            .picture(&canvas(), 1.0)
            .expect("the bottom scrim");
        assert_eq!(bottom.bounds.y + bottom.bounds.height, 1080.0);
        assert_eq!(bottom.bounds.y.fract(), 0.0);

        let doubled = Scrim::top().picture(&canvas(), 2.0).expect("the top scrim");
        assert_eq!(doubled.rows().len(), 2 * top.rows().len());
        assert_eq!(doubled.bounds.height, top.bounds.height);
    }

    /// `Scrims::for_canvas` holds the same images that each scrim builds.
    #[test]
    fn for_canvas_builds_the_picture_of_each_scrim() {
        let scrims = Scrims::for_canvas(&canvas(), 1.0);
        for (held, scrim) in [
            (scrims.top, Scrim::top()),
            (scrims.bottom, Scrim::bottom()),
            (scrims.cast, Scrim::cast()),
        ] {
            let held = held.expect("a scrim with rows");
            let fresh = scrim.picture(&canvas(), 1.0).expect("a scrim with rows");
            assert_eq!(held.bounds, fresh.bounds);
            assert_eq!(held.rows(), fresh.rows());
        }
    }

    /// A scale of zero leaves no output rows, so there is no image.
    #[test]
    fn a_scale_of_zero_makes_no_picture() {
        assert!(Scrim::top().picture(&canvas(), 0.0).is_none());
    }
}
