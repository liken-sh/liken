// The series page's front layer: the header at the top of the frame, and
// the wall of episode stills in the region under it. The header stands
// still, because it carries the focused episode's line and its plot, and
// the wall scrolls inside its own region, clipped to it, so no still and
// no divider draws over the header.
//
// The backdrop and the scrim under this layer cover the header alone, so
// the wall draws on the black ground and no art sits over art. The page's
// view, at the end of this file, stacks the backdrop, this layer, and the
// loading state's layer.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Point, Rectangle, Theme, mouse};

use super::super::Item;
use super::super::franchise::strips::Place;
use super::super::stripes::Face;
use super::layout::{self, Layout};
use super::{COLUMNS, Focus, Series, Still, seasons};
use crate::art::Art;
use crate::look;
use crate::views::across::Across;
use crate::views::curtain::{Curtain, Head, Layer};
use crate::views::layers;
use crate::views::{area, card, divider, people, rail, strip, text, wall};

// The header: its blocks, and where its button row and status line stand.
mod header;

pub(super) use header::{row_at, status_line, under};

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
    /// The second this frame draws, which the scrolls ease on.
    pub at: f64,
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
        // browser's strip does, so a focused bar, mark, or member draws as
        // focused only while the outline is on the page.
        let focus = self.held.then_some(series.focus);
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let store = &mut *self.store.borrow_mut();

        self.header(&mut frame, store, bounds, layout::header(bounds), focus);

        let wall = placed(series, bounds);
        let Placed {
            whole,
            region,
            inset,
            ..
        } = wall;
        let bands = &wall.layout.bands;
        let width = region.width - 2.0 * inset;
        let offset = series.scroll.toward(wall.offset, self.at);

        // The stills draw under the band a held divider keeps, and the
        // dividers over their own clip, because the renderer draws every
        // image of a layer over every fill of it, and a still that reached
        // the band would cross the divider's rule.
        frame.with_clip(layout::stills(region), |frame| {
            for (season, band) in bands.iter().enumerate() {
                if band.shows(offset, region.height) {
                    wall::draw(frame, store, &stills(series, &wall, season, offset, focus));
                }
            }
        });

        let dividers = area(region.x + inset, region.y, width, region.height);
        frame.with_clip(region, |frame| {
            for (season, band) in series.seasons.iter().zip(bands) {
                if !band.shows(offset, region.height) {
                    continue;
                }
                divider::draw(
                    frame,
                    layout::divider_box(dividers, band, offset),
                    &season.name,
                );
            }

            // Each row draws at the scroll its own ease has reached at
            // this second, toward the place this frame's focus rests it at.
            for (index, row) in rows(series, &wall, offset, focus).into_iter().enumerate() {
                let scroll = series.rows.at(index, row.scrolled(), self.at);
                row.at(scroll).draw(frame, store);
            }

            let mut at = Point::new(region.x + inset, region.y + wall.layout.foot - offset);
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
            let focus = match focus {
                Some(Focus::Rail(bar)) => Some(bar),
                _ => None,
            };
            rail::draw_at(frame, whole, &series.bars, focus, RAIL.0, RAIL.1);
        });

        vec![frame.into_geometry()]
    }
}

/// The side the seasons rail stands at, and how its bars fit the region.
pub(super) const RAIL: (rail::Side, rail::Fit<'static>) = (rail::Side::Right, rail::Fit::Fitted);

/// The wall's measures at these bounds: the region the rail and the wall
/// share, the region the wall keeps beside the rail, how far the rows
/// under the stills stand in from the region's edges, the layout of the
/// seasons and the rows, and how far the wall scrolls to rest with focus
/// in view.
pub(super) struct Placed {
    pub(super) whole: Rectangle,
    pub(super) region: Rectangle,
    inset: f32,
    layout: Layout,
    pub(super) offset: f32,
}

/// The wall of this series at these bounds, at rest.
pub(super) fn placed(series: &Series, bounds: Rectangle) -> Placed {
    // The rail takes the right edge of the region, and the wall keeps
    // the rest.
    let whole = layout::region(bounds);
    let region = rail::beside_at(whole, &series.bars, RAIL.0);
    let cells = wall::lined(region.width, wall::STILL, COLUMNS, card::LINES);
    let inset = (cells.width - cells.poster_width) / 2.0;
    let layout = Layout::of(
        &series.seasons,
        cells,
        series.franchises.bands().len(),
        series.stripes.bands().len(),
        series.foot.height(region.width - 2.0 * inset),
    );
    let offset = layout.scroll(seasons::standing(series), &series.seasons, region.height);
    Placed {
        whole,
        region,
        inset,
        layout,
        offset,
    }
}

/// The grid of one season's stills at this scroll down of the wall, with
/// focus where `focus` puts it. The band a held divider keeps stands over
/// the stills' own region, so it counts as content the wall has scrolled
/// past.
pub(super) fn stills<'a>(
    series: &'a Series,
    wall: &Placed,
    season: usize,
    offset: f32,
    focus: Option<Focus>,
) -> wall::Grid<'a, Still> {
    let run = series.seasons[season].run;
    let band = wall.layout.bands[season];
    let region = layout::stills(wall.region);
    wall::Grid {
        items: &series.stills[run.first..run.first + run.count],
        focus: match focus {
            Some(Focus::Still(index)) if index >= run.first && index < run.first + run.count => {
                Some(index - run.first)
            }
            _ => None,
        },
        library: &series.library,
        ratio: wall::STILL,
        columns: COLUMNS,
        lines: card::LINES,
        offset: offset + (region.y - wall.region.y) - band.rows_top,
        region,
    }
}

/// The rows the wall scrolls across, in the page's order: the franchise
/// strips, then the stripes of people, at this scroll down and with focus
/// where `focus` puts it.
pub(super) fn rows<'a>(
    series: &'a Series,
    wall: &Placed,
    offset: f32,
    focus: Option<Focus>,
) -> Vec<Across<'a, Item, Face>> {
    let Placed { region, inset, .. } = *wall;
    let place = |top: f32, height: f32| {
        area(
            region.x + inset,
            region.y + top - offset,
            region.width - 2.0 * inset,
            height,
        )
    };
    let mut rows = Vec::new();
    let franchises = series
        .franchises
        .bands()
        .iter()
        .zip(&wall.layout.franchises);
    for (index, (band, top)) in franchises.enumerate() {
        rows.push(Across::Strip(strip::Strip {
            room: None,
            members: &band.members,
            current: band.current,
            kept: None,
            focus: match focus {
                Some(Focus::Franchise(strip, Place::Member(member))) if strip == index => {
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
            region: place(*top, layout::strip_height()),
            scroll: None,
        }));
    }
    let stripes = series.stripes.bands().iter().zip(&wall.layout.stripes);
    for (index, (band, top)) in stripes.enumerate() {
        rows.push(Across::Stripe(people::Stripe {
            people: &band.faces,
            focus: match focus {
                Some(Focus::Stripe(stripe, slot)) if stripe == index => Some(slot),
                _ => None,
            },
            heading: band.heading,
            library: &series.library,
            region: place(*top, people::HEIGHT),
            scroll: None,
        }));
    }
    rows
}

/// The box the series' logo draws in at these bounds, which is where the
/// loading state starts the logo's move. The header stands at the top of
/// the frame whatever the wall under it has scrolled to.
pub fn head(bounds: Rectangle) -> Rectangle {
    area(
        layout::header(bounds).x,
        bounds.y + layout::TOP,
        layout::LOGO_WIDTH,
        layout::LOGO_HEIGHT,
    )
}

// The page as the browser draws it: the backdrop, this front layer, and the
// loading state's layer over both.
impl Series {
    /// The view: the backdrop behind the header, the scrim over it, the
    /// header and the wall over both, and the departing art of the
    /// loading state over all three while that state runs. The dim of the
    /// room and the curtain's front are the frame's own layers over this
    /// one. `at` is the second the frame draws, which the scrolls ease on.
    pub fn view<'a, A: Art>(
        &'a self,
        store: &'a RefCell<A>,
        curtain: Option<Curtain<'a>>,
        held: bool,
        at: f64,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        layers::Page {
            library: &self.library,
            art: &self.backdrop,
            store,
            ground: layers::Ground::Below(layout::head()),
            front: Page {
                series: self,
                store,
                lifted: curtain.is_some(),
                held,
                at,
            },
            over: curtain.map(|curtain| self.curtain(store, curtain)),
        }
        .view()
    }

    /// The curtain's front layer, which the frame draws over the dim.
    pub fn front<'a, A: Art>(
        &'a self,
        store: &'a RefCell<A>,
        curtain: Curtain<'a>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        layers::front(self.curtain(store, curtain))
    }

    // The loading state's layer for this page. Both the departing art
    // under the dim and the logo over it are drawn from it, so the two
    // read the same title, art, and clock.
    fn curtain<'a, A: Art>(&'a self, store: &'a RefCell<A>, curtain: Curtain<'a>) -> Layer<'a, A> {
        Layer {
            library: &self.library,
            art: &self.backdrop,
            logo: &self.logo,
            name: &self.title,
            store,
            head: self,
            curtain,
        }
    }
}

impl Head for Series {
    fn head(&self, bounds: Rectangle) -> Rectangle {
        head(bounds)
    }
}

#[cfg(test)]
mod tests;
