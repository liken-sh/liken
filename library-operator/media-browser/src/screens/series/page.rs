// The series page's front layer: the header at the top of the frame, and
// the wall of episode stills in the region under it. The header stands
// still, because it carries the focused episode's line and its plot, and
// the wall scrolls inside its own region, clipped to it, so no still and
// no divider draws over the header.
//
// The backdrop and the scrim under this layer cover the header alone, so
// the wall draws on the black ground and no art sits over art.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::layout::{self, Layout};
use super::{COLUMNS, Series};
use crate::look;
use crate::posters::Posters;
use crate::views::stack::Stack;
use crate::views::{area, divider, header, text, wall};

// The margin at both sides of the header's text.
const MARGIN: f32 = 120.0;

// The share of the width the column of text takes. The column ends inside
// the part of the scrim that holds its full shade, so every line reads
// over the art whatever the art holds.
const COLUMN: f32 = 0.42;

/// The page's front layer as one canvas.
pub struct Page<'a, P> {
    /// The series the page is about.
    pub series: &'a Series,
    /// The store the logo and the stills come from.
    pub posters: &'a RefCell<P>,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Page<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let series = self.series;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let posters = &mut *self.posters.borrow_mut();

        self.header(&mut frame, posters, layout::header(bounds));

        let region = layout::region(bounds);
        let cells = wall::cells(region.width, wall::STILL, COLUMNS);
        let layout = Layout::of(&series.seasons, cells);
        let offset = layout.scroll(series.focus, &series.seasons, region.height);

        frame.with_clip(region, |frame| {
            let inset = (cells.width - cells.poster_width) / 2.0;
            for (season, band) in series.seasons.iter().zip(&layout.bands) {
                if !band.shows(offset, region.height) {
                    continue;
                }
                divider::draw(
                    frame,
                    area(
                        inset,
                        region.y + band.top - offset,
                        region.width - 2.0 * inset,
                        divider::HEIGHT,
                    ),
                    &season.name,
                    &season.year,
                );
                let run = season.run;
                wall::draw(
                    frame,
                    posters,
                    &wall::Grid {
                        items: &series.stills[run.first..run.first + run.count],
                        focus: (series.focus >= run.first && series.focus < run.first + run.count)
                            .then(|| series.focus - run.first),
                        marked: true,
                        library: &series.library,
                        ratio: wall::STILL,
                        columns: COLUMNS,
                        region,
                        offset: offset - band.rows_top,
                    },
                );
            }
        });

        vec![frame.into_geometry()]
    }
}

impl<P: Posters> Page<'_, P> {
    // The header's blocks, from the logo down to the cast. Every block is
    // cut to its own lines, so a long title, a long line, or a long plot
    // never pushes the cast out of the header's fixed height. The focused
    // episode's line and plot stand in the place the series' plot takes on
    // a series whose episodes have not landed.
    fn header(&self, frame: &mut canvas::Frame<Renderer>, posters: &mut P, region: Rectangle) {
        let series = self.series;
        let column = region.width * COLUMN;
        let mut stack = Stack::new(Point::new(MARGIN, region.y + layout::TOP), layout::GAP);

        let title = area(stack.at().x, stack.at().y, column, layout::LOGO_HEIGHT);
        frame.with_clip(title, |frame| {
            header::title(
                frame,
                posters,
                &header::Title {
                    library: &series.library,
                    logo: &series.logo,
                    name: &series.title,
                    at: stack.at(),
                    logo_box: (layout::LOGO_WIDTH, layout::LOGO_HEIGHT),
                    width: column,
                    size: look::HEAD_TITLE,
                },
            );
        });
        stack.add(layout::LOGO_HEIGHT);

        let taken = text::block(
            frame,
            &series.facts,
            stack.at(),
            look::FACTS,
            look::muted(),
            column,
            1,
        );
        stack.add(taken);

        let (line, plot) = match series.stills.get(series.focus) {
            Some(still) => (still.facts.as_str(), still.plot.as_str()),
            None => ("", series.plot.as_str()),
        };
        let taken = text::block(
            frame,
            line,
            stack.at(),
            look::FACTS,
            look::text(),
            column,
            1,
        );
        stack.add(taken);
        let taken = text::block(
            frame,
            plot,
            stack.at(),
            look::PLOT,
            look::text(),
            column,
            layout::PLOT_LINES,
        );
        stack.add(taken);

        text::block(
            frame,
            &series.cast,
            stack.at(),
            look::CREDITS,
            look::muted(),
            column,
            layout::CAST_LINES,
        );
    }
}
