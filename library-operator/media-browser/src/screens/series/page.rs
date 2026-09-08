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

use super::super::franchise::strips::Place;
use super::layout::{self, Layout};
use super::{COLUMNS, Focus, Series, seasons};
use crate::art::Art;
use crate::look;
use crate::views::stack::Stack;
use crate::views::{
    area, card, curtain, divider, header, people, rail, ratings, strip, text, wall,
};

// The margin at both sides of the header's text.
const MARGIN: f32 = 120.0;

// The share of the width the column of text takes. The column ends inside
// the part of the scrim that holds its full shade, so every line reads
// over the art whatever the art holds.
const COLUMN: f32 = 0.42;

/// The page's front layer as one canvas.
pub struct Page<'a, A> {
    /// The series the page is about.
    pub series: &'a Series,
    /// The store the logo and the stills come from.
    pub store: &'a RefCell<A>,
    /// Whether the loading state has lifted the logo off the page, so the
    /// head leaves its box empty.
    pub lifted: bool,
    /// Whether the page holds focus, or the browser's strip over it does.
    pub held: bool,
}

impl<A: Art> canvas::Program<Infallible, Theme, Renderer> for Page<'_, A> {
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
        // The page's focus while the page holds it, and none while the
        // browser's strip does, so one mark draws on the glass.
        let focus = self.held.then_some(series.focus);
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let store = &mut *self.store.borrow_mut();

        self.header(&mut frame, store, bounds, layout::header(bounds));

        // The rail takes the right edge of the region, and the wall keeps
        // the rest.
        let whole = layout::region(bounds);
        let region = rail::beside_at(whole, &series.bars, rail::Side::Right);
        let cells = wall::lined(region.width, wall::STILL, COLUMNS, card::LINES);
        let inset = (cells.width - cells.poster_width) / 2.0;
        let width = region.width - 2.0 * inset;
        let layout = Layout::of(
            &series.seasons,
            cells,
            series.franchises.bands().len(),
            series.stripes.bands().len(),
            series.foot.height(width),
        );
        let offset = layout.scroll(seasons::standing(series), &series.seasons, region.height);

        // The stills draw under the band a held divider keeps, and the
        // dividers over their own clip, because the renderer draws every
        // image of a layer over every fill of it, and a still that reached
        // the band would cross the divider's rule. The band stands over the
        // stills' own region, so it counts as content the wall has
        // scrolled past.
        let stills = layout::stills(region);
        let scrolled = offset + (stills.y - region.y);
        frame.with_clip(stills, |frame| {
            for (season, band) in series.seasons.iter().zip(&layout.bands) {
                if !band.shows(offset, region.height) {
                    continue;
                }
                let run = season.run;
                wall::draw(
                    frame,
                    store,
                    &wall::Grid {
                        items: &series.stills[run.first..run.first + run.count],
                        focus: match focus {
                            Some(Focus::Still(index))
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
                        lines: card::LINES,
                        region: stills,
                        offset: scrolled - band.rows_top,
                    },
                );
            }
        });

        let dividers = area(inset, region.y, width, region.height);
        frame.with_clip(region, |frame| {
            for (season, band) in series.seasons.iter().zip(&layout.bands) {
                if !band.shows(offset, region.height) {
                    continue;
                }
                divider::draw(
                    frame,
                    layout::divider_box(dividers, band, offset),
                    &season.name,
                );
            }

            for (index, (band, top)) in series
                .franchises
                .bands()
                .iter()
                .zip(&layout.franchises)
                .enumerate()
            {
                strip::draw(
                    frame,
                    store,
                    &strip::Strip {
                        letters: &[],
                        circled: false,
                        members: &band.members,
                        current: band.current,
                        focus: match focus {
                            Some(Focus::Franchise(strip, Place::Member(member)))
                                if strip == index =>
                            {
                                Some(member)
                            }
                            _ => None,
                        },
                        heading: &band.heading,
                        library: &series.library,
                        last: None,
                        lines: card::LINES,
                        headed: matches!(
                            focus,
                            Some(Focus::Franchise(strip, Place::Heading)) if strip == index
                        ),
                        region: area(
                            inset,
                            region.y + top - offset,
                            region.width - 2.0 * inset,
                            layout::strip_height(),
                        ),
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
                    store,
                    &people::Stripe {
                        people: &band.faces,
                        focus: match focus {
                            Some(Focus::Stripe(stripe, slot)) if stripe == index => Some(slot),
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

        frame.with_clip(layout::rail_clip(whole), |frame| {
            rail::draw_at(
                frame,
                whole,
                &series.bars,
                match focus {
                    Some(Focus::Rail(bar)) => Some(bar),
                    _ => None,
                },
                rail::Side::Right,
                rail::Fit::Fitted,
            );
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

impl<A: Art> Page<'_, A> {
    // The header's blocks, from the logo down to the plot. Every block is
    // cut to its own lines, so a long title, a long line, or a long plot
    // never pushes the header past its fixed height. The focused
    // episode's line and plot stand in the place the series' plot takes on
    // a series whose episodes have not landed, and while a stripe holds
    // focus.
    fn header(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        store: &mut A,
        bounds: Rectangle,
        region: Rectangle,
    ) {
        let series = self.series;
        let column = region.width * COLUMN;
        let mut stack = Stack::new(Point::new(MARGIN, region.y + layout::TOP), layout::GAP);

        let title = area(stack.at().x, stack.at().y, column, layout::LOGO_HEIGHT);
        frame.with_clip(title, |frame| {
            header::title(
                frame,
                store,
                &header::Title {
                    library: &series.library,
                    logo: &series.logo,
                    name: &series.title,
                    at: stack.at(),
                    logo_box: (layout::LOGO_WIDTH, layout::LOGO_HEIGHT),
                    decode: curtain::logo_decode(bounds),
                    width: column,
                    size: look::HEAD_TITLE,
                    lifted: self.lifted,
                },
            );
        });
        stack.add(layout::LOGO_HEIGHT);

        // The facts line is one line, cut with an ellipsis where a long
        // list of genres runs past the column, so it never ends on a comma.
        let taken = text::block(
            frame,
            &text::measured_cut(&series.facts, look::FACTS, column),
            stack.at(),
            look::FACTS,
            look::muted(),
            column,
            1,
        );
        stack.add(taken);

        let taken = ratings::draw(frame, &series.ratings, stack.at());
        stack.add(taken);

        let (line, aired, plot) = match series.focused() {
            Some(still) => (
                still.facts.as_str(),
                still.aired.as_str(),
                still.plot.as_str(),
            ),
            None => ("", "", series.plot.as_str()),
        };
        // The episode's name is cut to one line with an ellipsis, and the
        // runtime and the air date take a line of their own under it, so
        // a long name never pushes the date out of the header.
        let taken = text::block(
            frame,
            &text::cut(line, look::FACTS, column),
            stack.at(),
            look::FACTS,
            look::text(),
            column,
            1,
        );
        stack.add(taken);
        let taken = text::block(
            frame,
            aired,
            stack.at(),
            look::FACTS,
            look::muted(),
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
