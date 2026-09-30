//! The dark shade at the top and the foot of the screen, behind the header
//! and the scrubber, so their text reads against one background whatever
//! the frame behind it.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use super::Canvas;
use crate::theme;

/// Which side of the screen a scrim is dark on. The far side fades to clear.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Edge {
    Top,
    Bottom,
}

/// One scrim: the shade holds at its peak from the screen edge over the
/// text, then eases to clear with one smoothstep over the rest of its reach.
/// One ease over the whole fade has no flat spot inside it, and the eye
/// reads every flat spot in a fade as the edge of a band.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Scrim {
    edge: Edge,
    /// How far from the screen edge the peak holds, in canvas rows.
    hold: f32,
    /// How far from the screen edge the shade is clear, in canvas rows.
    clear: f32,
}

impl Scrim {
    /// The scrim behind the header and the clock.
    pub fn top() -> Self {
        Self {
            edge: Edge::Top,
            hold: theme::SCRIM_TOP_HOLD,
            clear: theme::SCRIM_TOP_CLEAR,
        }
    }

    /// The scrim behind the scrubber and the strip.
    pub fn bottom() -> Self {
        Self {
            edge: Edge::Bottom,
            hold: theme::SCRIM_BOTTOM_HOLD,
            clear: theme::SCRIM_BOTTOM_CLEAR,
        }
    }

    /// The fraction of the ground the scrim covers at one canvas row.
    pub fn opacity_at(&self, row: f32) -> f32 {
        let from_edge = match self.edge {
            Edge::Top => row,
            Edge::Bottom => theme::CANVAS_HEIGHT - row,
        };
        let t = ((from_edge - self.hold) / (self.clear - self.hold)).clamp(0.0, 1.0);
        theme::alpha::SCRIM_EDGE * (1.0 - t * t * (3.0 - 2.0 * t))
    }

    /// The scrim as the picture the display draws: one texel wide, with one
    /// row for each output row it covers, stretched across the width. A
    /// picture costs one texture read per pixel, where the toolkit's gradient
    /// fill searches its stops at every pixel, and the scrims cover about
    /// half the screen.
    ///
    /// `density` is the window's scale factor, the output pixels one logical
    /// pixel covers. The picture starts at the screen edge, so each of its
    /// rows lands on one output row and carries the value at that row's
    /// middle.
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

/// The pictures of both scrims, for one canvas and one scale factor. Only the
/// fade changes between two frames, and the picture takes the fade as its
/// opacity, so the pictures are resolved once per surface.
#[derive(Debug, Clone, Default)]
pub struct Scrims {
    pub top: Option<Picture>,
    pub bottom: Option<Picture>,
}

impl Scrims {
    pub fn for_canvas(canvas: &Canvas, density: f32) -> Self {
        Self {
            top: Scrim::top().picture(canvas, density),
            bottom: Scrim::bottom().picture(canvas, density),
        }
    }
}

/// One scrim as the toolkit draws it: the column of texels, and the canvas
/// rectangle it stretches over.
#[derive(Debug, Clone)]
pub struct Picture {
    pub handle: Handle,
    pub bounds: Rectangle,
}

impl Picture {
    /// The alpha byte of each row, top to bottom. A test reads the picture
    /// through this and not through the handle, because a handle takes an id
    /// of its own on every call.
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

    /// Each scrim holds its peak over the text at the screen edge and is
    /// clear by the end of its reach.
    #[test]
    fn a_scrim_holds_its_peak_at_the_edge_and_clears_by_its_reach() {
        let peak = theme::alpha::SCRIM_EDGE;
        let (top, bottom) = (Scrim::top(), Scrim::bottom());
        assert_eq!(top.opacity_at(0.0), peak);
        assert_eq!(top.opacity_at(theme::SCRIM_TOP_HOLD), peak);
        assert_eq!(top.opacity_at(theme::SCRIM_TOP_CLEAR), 0.0);
        assert_eq!(bottom.opacity_at(1080.0), peak);
        assert_eq!(bottom.opacity_at(1080.0 - theme::SCRIM_BOTTOM_HOLD), peak);
        assert_eq!(bottom.opacity_at(1080.0 - theme::SCRIM_BOTTOM_CLEAR), 0.0);
    }

    /// Across the fade, the change from one output row to the next grows to
    /// one steepest row and then shrinks, with no flat spot between. A flat
    /// spot inside a fade reads as the edge of a band.
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

    /// The picture is one texel of the shadow colour per row.
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

    /// The picture covers the whole width from the screen edge, one row per
    /// output row, and a window at twice the scale carries twice the rows
    /// over the same canvas rows.
    #[test]
    fn the_picture_lands_on_whole_output_rows_from_the_screen_edge() {
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

    /// Both scrims of one surface are the pictures its two scrims resolve to.
    #[test]
    fn the_scrims_of_one_surface_are_its_two_pictures() {
        let scrims = Scrims::for_canvas(&canvas(), 1.0);
        for (held, scrim) in [(scrims.top, Scrim::top()), (scrims.bottom, Scrim::bottom())] {
            let held = held.expect("a scrim with rows");
            let fresh = scrim.picture(&canvas(), 1.0).expect("a scrim with rows");
            assert_eq!(held.bounds, fresh.bounds);
            assert_eq!(held.rows(), fresh.rows());
        }
    }

    /// A scale of nothing would divide every row of the picture away, so it
    /// makes no picture.
    #[test]
    fn a_scale_of_nothing_makes_no_picture() {
        assert!(Scrim::top().picture(&canvas(), 0.0).is_none());
    }
}
