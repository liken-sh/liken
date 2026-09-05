// The jump rail: one rotated bar per stretch of rows, at the left of a long
// wall, so a person crosses a hundred rows in two presses. A bar names a
// stretch by its first and its last row, and the rail reads no further into
// what the stretch is: a franchise's eras, a series' seasons, and a wall's
// years are all bars here. Bars that overlap draw in lanes, the caller
// deciding which lane each one takes, and the rail draws at most LANES of
// them. The words read bottom to top, because a bar is tall and narrow.

use std::f32::consts::FRAC_PI_2;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle, Vector};

use super::{area, label, mark, rounded, stack, text};
use crate::look;

/// One stretch of rows: the words on it, the first and the last row it covers,
/// and the lane it draws in. `first` and `last` are row indices in the wall
/// the rail stands beside.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Bar {
    pub label: String,
    pub first: usize,
    pub last: usize,
    pub lane: usize,
}

/// The width of one lane.
pub const LANE: f32 = 44.0;

/// The most lanes a rail draws. A third lane leaves the wall no room,
/// so a bar the caller puts deeper draws in the last one.
pub const LANES: usize = 2;

// The space under a bar and to the right of its lane, so two bars in
// one lane read as two and the wall stands clear of the rail.
const GAP: f32 = 8.0;

// The radius the bars are drawn with.
const ROUND: f32 = 8.0;

// The room the label's box holds past the length the average advance
// measures, so the shaper's own line always fits inside it.
const SLACK: f32 = 24.0;

/// The width a rail of these bars takes, and none where it holds no
/// bars.
pub fn width(bars: &[Bar]) -> f32 {
    lanes(bars) as f32 * LANE
}

/// How many lanes these bars fill, at most [`LANES`].
pub fn lanes(bars: &[Bar]) -> usize {
    bars.iter()
        .map(|bar| bar.lane.min(LANES - 1) + 1)
        .max()
        .unwrap_or(0)
}

/// The box one bar draws in, in frame space after the scroll. `tops` is
/// where every row of the wall beside the rail starts, and where the last
/// one ends, from the region's top, because those rows are not one
/// height.
pub fn bar(region: Rectangle, held: &Bar, tops: &[f32], offset: f32) -> Rectangle {
    let lane = held.lane.min(LANES - 1) as f32;
    let top = tops.get(held.first).copied().unwrap_or_default();
    let end = tops
        .get(held.last + 1)
        .or(tops.last())
        .copied()
        .unwrap_or(top);
    area(
        region.x + lane * LANE,
        region.y + top - offset,
        LANE - GAP,
        (end - top - GAP).max(0.0),
    )
}

/// Draw the rail. `focus` names the bar that holds focus, or nothing
/// while the wall beside it holds focus. Only the bars the region
/// reaches become geometry.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    region: Rectangle,
    bars: &[Bar],
    tops: &[f32],
    offset: f32,
    focus: Option<usize>,
) {
    for (index, held) in bars.iter().enumerate() {
        let bounds = bar(region, held, tops, offset);
        if bounds.y + bounds.height < region.y || bounds.y > region.y + region.height {
            continue;
        }
        frame.fill(&rounded(bounds, ROUND), look::slot());
        written(frame, bounds, region, &held.label);
        if focus == Some(index) {
            mark(frame, bounds);
        }
    }
}

/// Where one bar's label draws: the head of a section, not the middle of the
/// bar. An era of a hundred rows is taller than any screen, so a label at the
/// bar's own middle is off screen for most of the scroll and the rail reads
/// blank. The label sits at the bar's top end while that end is on screen,
/// pins to the top of the region while the bar runs past it, and the bar's
/// bottom end pushes it off, so a label never leaves its own bar. `length` is
/// the label's own length along the bar, and a bar shorter than that carries
/// as much of the label as it holds.
pub fn label_box(bounds: Rectangle, region: Rectangle, length: f32) -> Rectangle {
    stack::held(bounds, region, length)
}

// One bar's words, turned a quarter circle so they read from the foot
// of the bar to its head. The turn puts the text on the path renderer,
// which draws it as a mesh and not as a line of text, so the words go
// into the same buffer as the bar under them and draw over it. A clip
// of their own would take them out of that buffer and the bar would
// then cover them, so the words are cut to the bar's own length
// instead.
fn written(
    frame: &mut canvas::Frame<Renderer>,
    bounds: Rectangle,
    region: Rectangle,
    content: &str,
) {
    let shown = text::cut(content, look::HEADING, bounds.height);
    let at = label_box(bounds, region, text::width(&shown, look::HEADING) + SLACK);
    frame.with_save(|frame| {
        frame.translate(Vector::new(at.center_x(), at.center_y()));
        frame.rotate(-FRAC_PI_2);
        frame.fill_text(label(
            &shown,
            Point::ORIGIN,
            look::HEADING,
            look::text(),
            Alignment::Center,
            Vertical::Center,
            // The words are cut to the bar already, and a width the
            // shaper may exceed would wrap them into a second line that
            // draws across the first once the bar turns them.
            f32::INFINITY,
        ));
    });
}

/// The part of the region the wall beside the rail draws in: everything
/// to the right of the last lane.
pub fn beside(region: Rectangle, bars: &[Bar]) -> Rectangle {
    let taken = width(bars);
    area(
        region.x + taken,
        region.y,
        region.width - taken,
        region.height,
    )
}

/// The bar that covers one row, and the first one where two lanes cover
/// it, so a move onto the rail lands on the widest stretch.
pub fn covering(bars: &[Bar], row: usize) -> Option<usize> {
    bars.iter()
        .position(|bar| bar.first <= row && row <= bar.last)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bars() -> Vec<Bar> {
        vec![
            Bar {
                label: "The Long Survey".into(),
                first: 0,
                last: 7,
                lane: 0,
            },
            Bar {
                label: "The Coppice Years".into(),
                first: 4,
                last: 5,
                lane: 1,
            },
        ]
    }

    fn region() -> Rectangle {
        area(100.0, 200.0, 1720.0, 800.0)
    }

    #[test]
    fn a_rail_takes_one_lane_for_every_lane_its_bars_fill() {
        assert_eq!(lanes(&bars()), 2);
        assert_eq!(width(&bars()), 2.0 * LANE);
        assert_eq!(lanes(&bars()[..1]), 1);
        assert_eq!(width(&[]), 0.0);
    }

    // The tops of this many rows of one height, and where the last ends.
    fn tops(rows: usize, height: f32) -> Vec<f32> {
        (0..=rows).map(|row| row as f32 * height).collect()
    }

    #[test]
    fn a_bar_deeper_than_the_last_lane_draws_in_the_last_lane() {
        let deep = [Bar {
            label: "Deeper".into(),
            first: 0,
            last: 1,
            lane: 4,
        }];
        assert_eq!(lanes(&deep), LANES);
        assert_eq!(
            bar(region(), &deep[0], &tops(8, 100.0), 0.0).x,
            region().x + LANE
        );
    }

    #[test]
    fn a_bar_stands_over_the_rows_it_covers() {
        let rows = tops(8, 100.0);
        let survey = bar(region(), &bars()[0], &rows, 0.0);
        assert_eq!(survey.x, region().x);
        assert_eq!(survey.y, region().y);
        assert_eq!(survey.height, 8.0 * 100.0 - GAP);
        assert_eq!(survey.width, LANE - GAP);

        let years = bar(region(), &bars()[1], &rows, 0.0);
        assert_eq!(years.x, region().x + LANE);
        assert_eq!(years.y, region().y + 4.0 * 100.0);
        assert_eq!(years.height, 2.0 * 100.0 - GAP);
    }

    #[test]
    fn a_bar_over_rows_of_two_heights_reaches_the_end_of_its_last() {
        let rows = [0.0, 300.0, 350.0, 650.0];
        let over = bar(region(), &bars()[0], &rows, 0.0);
        assert_eq!(over.height, 650.0 - GAP);
        let short = bar(
            region(),
            &Bar {
                first: 1,
                last: 1,
                ..Bar::default()
            },
            &rows,
            0.0,
        );
        assert_eq!(short.y, region().y + 300.0);
        assert_eq!(short.height, 50.0 - GAP);
        assert_eq!(bar(region(), &bars()[0], &[], 0.0).height, 0.0);
    }

    #[test]
    fn a_scrolled_rail_moves_with_its_wall() {
        let scrolled = bar(region(), &bars()[1], &tops(8, 100.0), 250.0);
        assert_eq!(scrolled.y, region().y + 4.0 * 100.0 - 250.0);
    }

    #[test]
    fn the_wall_stands_to_the_right_of_the_last_lane() {
        let wall = beside(region(), &bars());
        assert_eq!(wall.x, region().x + 2.0 * LANE);
        assert_eq!(wall.width, region().width - 2.0 * LANE);
        assert_eq!(wall.x + wall.width, region().x + region().width);
        assert_eq!(beside(region(), &[]), region());
    }

    #[test]
    fn a_label_sits_at_the_top_of_its_bar_while_that_end_is_on_screen() {
        let region = region();
        let bar = area(region.x, region.y + 40.0, LANE, 2000.0);
        let at = label_box(bar, region, 120.0);
        assert_eq!(at.y, bar.y);
        assert_eq!(at.height, 120.0);
    }

    #[test]
    fn a_label_pins_to_the_top_of_the_region_while_its_bar_runs_past_it() {
        let region = region();
        let bar = area(region.x, region.y - 3000.0, LANE, 9000.0);
        assert_eq!(label_box(bar, region, 120.0).y, region.y);
    }

    #[test]
    fn the_bottom_end_of_a_bar_pushes_its_label_off_with_it() {
        let region = region();
        let bar = area(region.x, region.y - 3000.0, LANE, 3060.0);
        let at = label_box(bar, region, 120.0);
        assert_eq!(at.y, bar.y + bar.height - 120.0);
        assert!(at.y < region.y);
    }

    #[test]
    fn a_label_whose_bar_has_ended_above_the_region_is_not_drawn_at_the_top() {
        let region = region();
        let bar = area(region.x, region.y - 3000.0, LANE, 2900.0);
        let at = label_box(bar, region, 120.0);
        assert_eq!(at.y + at.height, bar.y + bar.height);
        assert!(at.y + at.height < region.y);
    }

    #[test]
    fn a_bar_shorter_than_its_label_carries_what_it_holds() {
        let region = region();
        let bar = area(region.x, region.y + 10.0, LANE, 60.0);
        let at = label_box(bar, region, 120.0);
        assert_eq!(at.y, bar.y);
        assert_eq!(at.height, bar.height);
    }

    #[test]
    fn a_label_of_a_bar_over_a_few_rows_stays_inside_those_rows() {
        // A bar over rows 0 to 100 of a wall of 100-tall rows, with the
        // region over rows 40 to 43.
        let region = area(100.0, 200.0, 1720.0, 300.0);
        let bar = bar(
            region,
            &Bar {
                label: "The Long Saga".into(),
                first: 0,
                last: 100,
                lane: 0,
            },
            &tops(101, 100.0),
            4000.0,
        );
        let at = label_box(bar, region, 120.0);
        assert!(at.y >= region.y);
        assert!(at.y + at.height <= region.y + region.height);
    }

    #[test]
    fn a_move_onto_the_rail_lands_on_the_widest_bar_over_the_row() {
        assert_eq!(covering(&bars(), 0), Some(0));
        assert_eq!(covering(&bars(), 4), Some(0));
        assert_eq!(covering(&bars(), 9), None);
        assert_eq!(covering(&[], 0), None);
    }
}
