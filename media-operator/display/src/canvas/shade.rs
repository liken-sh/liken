// A soft darkening with no edge, which the track panel sets under its
// words so they read over a bright picture without a plate. Its alpha is
// the product of a ramp in from each side, each a smoothstep, so it has no
// line where it starts.
//
// It is drawn as a stack of strips, each one pixel tall with an alpha for
// each column, which the frame stretches over a few rows, as the scrims
// stretch one column across the width. A whole picture of the darkening is
// a few megabytes, and iced_wgpu uploads a picture of 2 MiB or more on a
// worker thread and draws it a frame late (`MAX_SYNC` in `raster.rs`), so
// the panel's first frame would have no darkening. The strips are each far
// under that size, and the stretch takes a quarter of the memory.
//
// The darkening needs a surface whose alpha has 8 bits. On a surface with
// 2 bits, every translucent pixel rounds to a quarter step, and the
// darkening draws as flat bands with edges. The open problem "The display
// cannot choose its surface format" has the cause.

use std::cell::RefCell;
use std::collections::HashMap;

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use crate::theme;

/// The rows of output pixels that each strip covers. The vertical ramp
/// changes by about one level of 255 over this many rows, so the steps
/// between strips do not show.
const STRIP_ROWS: f32 = 4.0;

/// The alpha at `at` units into a span of `length` whose ramps are `reach`
/// long: 0 at either end and `peak` between the ramps.
pub fn alpha(at: f32, length: f32, reach: f32, peak: f32) -> f32 {
    peak * ramp(at, reach).min(ramp(length - at, reach))
}

/// A smoothstep from 0, `into` units from an edge, to 1, `reach` units in.
/// A reach of 0 is no ramp, for a side at the frame's edge.
fn ramp(into: f32, reach: f32) -> f32 {
    if reach <= 0.0 {
        return 1.0;
    }
    let share = (into / reach).clamp(0.0, 1.0);
    share * share * (3.0 - 2.0 * share)
}

/// How far the darkening's ramps reach in from each edge: the sides, the
/// top, and the bottom.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Reach {
    pub sides: f32,
    pub top: f32,
    pub bottom: f32,
}

/// The strips of one darkening, each its bounds and its picture.
type Strips = Vec<(Rectangle, Handle)>;

thread_local! {
    static SHADES: RefCell<HashMap<[u32; 7], Strips>> =
        RefCell::new(HashMap::new());
}

/// The strips of a darkening over `bounds`, with ramps as long as `reach`
/// says, at `per_unit` output pixels for each canvas unit. They are made
/// once for each size and kept.
pub fn strips(bounds: Rectangle, reach: Reach, peak: f32, per_unit: f32) -> Strips {
    let (width, height) = (bounds.width, bounds.height);
    let key = [
        width,
        height,
        reach.sides,
        reach.top,
        reach.bottom,
        peak,
        per_unit,
    ]
    .map(f32::to_bits);
    let made = SHADES.with_borrow_mut(|shades| {
        shades
            .entry(key)
            .or_insert_with(|| {
                let columns = (width * per_unit).ceil().max(1.0) as u32;
                let strip = STRIP_ROWS / per_unit;
                let count = (height / strip).ceil() as u32;
                let shadow = (theme::color::SHADOW.r * 255.0).round() as u8;
                (0..count)
                    .map(|index| {
                        let top = index as f32 * strip;
                        let middle = top + strip / 2.0;
                        let across =
                            ramp(middle, reach.top).min(ramp(height - middle, reach.bottom));
                        let pixels: Vec<u8> = (0..columns)
                            .flat_map(|column| {
                                let x = (column as f32 + 0.5) / per_unit;
                                let a = alpha(x, width, reach.sides, peak) * across;
                                [shadow, shadow, shadow, (a * 255.0).round() as u8]
                            })
                            .collect();
                        let shape = Rectangle::new(
                            Point::new(0.0, top),
                            Size::new(width, strip.min(height - top)),
                        );
                        (shape, Handle::from_rgba(columns, 1, pixels))
                    })
                    .collect()
            })
            .clone()
    });
    made.into_iter()
        .map(|(shape, handle)| {
            let placed = Rectangle {
                x: shape.x + bounds.x,
                y: shape.y + bounds.y,
                ..shape
            };
            (placed, handle)
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The darkening is clear at its edges and at its peak between the
    /// ramps, and it rises without a step.
    #[test]
    fn the_darkening_rises_from_nothing_at_each_edge() {
        assert_eq!(alpha(0.0, 1000.0, 200.0, 0.5), 0.0);
        assert_eq!(alpha(1000.0, 1000.0, 200.0, 0.5), 0.0);
        assert_eq!(alpha(500.0, 1000.0, 200.0, 0.5), 0.5);
        assert_eq!(alpha(100.0, 1000.0, 200.0, 0.5), 0.25);
        let steps: Vec<f32> = (0..=200)
            .map(|at| alpha(at as f32, 1000.0, 200.0, 0.5))
            .collect();
        assert!(
            steps.windows(2).all(|pair| pair[1] - pair[0] < 0.004),
            "the ramp rises by more than 1/255 in one unit, which shows as a band"
        );
    }

    /// The strips cover the darkening's bounds from top to bottom, and a
    /// darkening of one size is made once.
    #[test]
    fn the_strips_cover_the_darkening_once() {
        let bounds = Rectangle::new(Point::new(100.0, 50.0), Size::new(400.0, 202.0));
        let reach = Reach {
            sides: 100.0,
            top: 20.0,
            bottom: 40.0,
        };
        let strips = strips(bounds, reach, 0.5, 1.0);
        assert_eq!(strips.len(), 51);
        assert_eq!(strips[0].0.y, 50.0);
        let last = strips[50].0;
        assert_eq!(last.y + last.height, 252.0);
        assert_eq!(strips[3].1, super::strips(bounds, reach, 0.5, 1.0)[3].1);
    }
}
