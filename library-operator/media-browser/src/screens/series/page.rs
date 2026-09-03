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
use super::{COLUMNS, Focus, Series};
use crate::look;
use crate::posters::Posters;
use crate::views::stack::Stack;
use crate::views::{area, divider, header, people, ratings, text, wall};

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
    /// Whether the loading state has lifted the logo off the page, so the
    /// head leaves its box empty.
    pub lifted: bool,
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
        let inset = (cells.width - cells.poster_width) / 2.0;
        let width = region.width - 2.0 * inset;
        let layout = Layout::of(
            &series.seasons,
            cells,
            series.stripes.bands().len(),
            series.foot.height(width),
        );
        let offset = layout.scroll(series.focus, &series.seasons, region.height);

        frame.with_clip(region, |frame| {
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
                );
                let run = season.run;
                wall::draw(
                    frame,
                    posters,
                    &wall::Grid {
                        items: &series.stills[run.first..run.first + run.count],
                        focus: match series.focus {
                            Focus::Still(index)
                                if index >= run.first && index < run.first + run.count =>
                            {
                                Some(index - run.first)
                            }
                            _ => None,
                        },
                        marked: true,
                        library: &series.library,
                        ratio: wall::STILL,
                        columns: COLUMNS,
                        lines: 1,
                        region,
                        offset: offset - band.rows_top,
                    },
                );
            }

            for (index, (band, top)) in series
                .stripes
                .bands()
                .iter()
                .zip(&layout.stripes)
                .enumerate()
            {
                people::draw(
                    frame,
                    posters,
                    &people::Stripe {
                        people: &band.faces,
                        focus: match series.focus {
                            Focus::Stripe(stripe, slot) if stripe == index => Some(slot),
                            _ => None,
                        },
                        heading: band.heading,
                        library: &series.library,
                        region: area(
                            inset,
                            region.y + top - offset,
                            region.width - 2.0 * inset,
                            people::HEIGHT,
                        ),
                    },
                );
            }

            let mut at = Point::new(inset, region.y + layout.foot - offset);
            for row in series.foot.rows() {
                at.y += row.lead;
                let color = match row.faint {
                    true => look::faint(),
                    false => look::text(),
                };
                text::line(frame, row.prefix, at, row.size, look::faint(), width);
                let after = Point::new(at.x + row.indent(), at.y);
                at.y += text::line(
                    frame,
                    row.content,
                    after,
                    row.size,
                    color,
                    width - row.indent(),
                );
            }
        });

        vec![frame.into_geometry()]
    }
}

/// The box the series' logo draws in at these bounds, which is where the
/// loading state starts the logo's move. The header stands at the top of
/// the frame whatever the wall under it has scrolled to.
pub fn head(bounds: Rectangle) -> Rectangle {
    area(
        MARGIN,
        bounds.y + layout::TOP,
        layout::LOGO_WIDTH,
        layout::LOGO_HEIGHT,
    )
}

impl<P: Posters> Page<'_, P> {
    // The header's blocks, from the logo down to the plot. Every block is
    // cut to its own lines, so a long title, a long line, or a long plot
    // never pushes the header past its fixed height. The focused
    // episode's line and plot stand in the place the series' plot takes on
    // a series whose episodes have not landed, and while a stripe holds
    // focus.
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
                    lifted: self.lifted,
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

        let taken = ratings::draw(frame, &series.ratings, stack.at());
        stack.add(taken);

        let (line, plot) = match series.focused() {
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
    }
}
