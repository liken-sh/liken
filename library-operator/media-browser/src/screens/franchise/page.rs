// The franchise page's one canvas: the rail and the time labels at the
// left, and the metro strip and one lane of cards beside them. The wall
// scrolls inside its own region and is clipped to it, so no row draws
// over the band, and the legend holds the top of the lane while the rows
// scroll under it. A row the region does not reach builds no geometry,
// so a wall of a hundred rows costs only the rows a person sees.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::image::FilterMethod;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::card::{self, GROUND_TONE};
use super::metro;
use super::wall::{self, Cell};
use super::{Focus, Franchise};
use crate::catalog::franchise::Standing;
use crate::look;
use crate::posters::Posters;
use crate::views::{Tone, area, artwork, band, extent, mark, rail, rounded, text, wall as still};

// The margin at both sides of the page.
const MARGIN: f32 = 80.0;

// The space between the band and the legend.
const TOP: f32 = 24.0;

// The space between a title's last line and the note under it.
const LEAD: f32 = 2.0;

// The space inside a thin row, from its edge to its words.
const INSET: f32 = 12.0;

// The dash and the space of a thin row's outline, and the width of its
// stroke.
const DASH: [f32; 2] = [6.0, 4.0];
const OUTLINE: f32 = 1.0;

// The radius of a thin row's corners.
const ROUND: f32 = 4.0;

/// The part of the frame the wall scrolls in: under the band, and inside
/// the margins.
pub fn region(bounds: Rectangle) -> Rectangle {
    let top = band::HEIGHT + TOP;
    area(
        MARGIN,
        top,
        (bounds.width - 2.0 * MARGIN).max(0.0),
        (bounds.height - top).max(0.0),
    )
}

/// The page's one canvas.
pub struct Page<'a, P> {
    /// The franchise the page is about.
    pub franchise: &'a Franchise,
    /// The store the entries' art comes from.
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
        let page = self.franchise;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let posters = &mut *self.posters.borrow_mut();

        let region = region(bounds);
        let rows = &page.rows;
        // The lane measures where the rail leaves off, the time label's
        // column, the strip, and the cards, and centers the cards where
        // nothing stands at the left.
        let wall::Lane {
            wall,
            columned,
            strip,
            cards,
        } = wall::Lane::of(region, &page.eras, rows, page.universes.len());
        let art = wall::art_height(region.height - wall::HEAD);
        let tops = wall::tops(rows, art);
        let down = match page.focus {
            Focus::Row(row) => wall::scroll(row, &tops, region.height),
            Focus::Rail(bar) => match page.eras.get(bar) {
                Some(bar) => wall::scroll(bar.first, &tops, region.height),
                None => 0.0,
            },
        };

        frame.with_clip(region, |frame| {
            // The rows start under the legend, and the tops carry that
            // head, so the rail reads them as they are.
            rail::draw(
                frame,
                region,
                &page.eras,
                &tops,
                down,
                match page.focus {
                    Focus::Rail(bar) => Some(bar),
                    _ => None,
                },
            );

            for (index, row) in rows.iter().enumerate() {
                let label = wall::time_box(wall, index, &tops, down);
                if label.y + label.height < region.y || label.y > region.y + region.height {
                    continue;
                }
                text::line(
                    frame,
                    &row.time,
                    label.position(),
                    look::CAPTION,
                    look::muted(),
                    wall::TIME - wall::GAP,
                );
            }

            // The strip and the rows are clipped to their own part of the
            // region, so a row that has scrolled up draws nothing over
            // the legend.
            let cells = wall::clipped(columned);
            frame.with_clip(cells, |frame| {
                metro::draw(frame, strip, rows, &tops, down);
                for (index, row) in rows.iter().enumerate() {
                    let bounds = wall::cell_box(cards, index, &tops, down);
                    if outside(bounds, cells) {
                        continue;
                    }
                    match row.cell.held() {
                        true => entry(frame, posters, &row.cell, bounds, cells, art),
                        false => thin(frame, &row.cell, bounds),
                    }
                    if page.focus == Focus::Row(index) {
                        mark(frame, bounds);
                    }
                }
            });

            // The legend holds the top of the lane while the wall scrolls
            // under it.
            let band = wall::banded(columned);
            frame.with_clip(band, |frame| {
                frame.fill_rectangle(band.position(), extent(band), look::BACKGROUND);
                metro::legend(
                    frame,
                    area(
                        columned.x,
                        wall::pinned(columned, down),
                        columned.width,
                        band.height,
                    ),
                    &page.universes,
                );
            });
        });

        vec![frame.into_geometry()]
    }
}

// Whether a row falls above or below the part of the frame the lane
// draws in, which then builds no geometry for it.
fn outside(cell: Rectangle, columned: Rectangle) -> bool {
    cell.y + cell.height < columned.y || cell.y > columned.y + columned.height
}

// One held entry of the order as a card of three layers: the ground made
// from its own art, the sharp art at the left, and the words beside the
// art. The ground draws first, then the art, then the words; the images
// of one layer draw in the order the canvas drew them, so the sharp art
// lands over the ground.
fn entry<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    cell: &Cell,
    bounds: Rectangle,
    clip: Rectangle,
    art: f32,
) {
    ground(frame, posters, cell, bounds, clip);
    let box_of = card::art_box(bounds, art);
    let art = match cell.wide {
        true => box_of,
        false => card::poster_box(box_of),
    };
    artwork(
        frame,
        posters,
        &cell.library,
        &cell.art,
        art,
        "",
        Tone::Full,
    );
    words(frame, cell, card::words_box(bounds, art));
}

// A thin row for an entry no library holds: a dashed outline, the title
// and the year from the left, and the note at the right. The note is the
// accent for a title still to come, so a person reads at a glance what
// is not out yet.
fn thin(frame: &mut canvas::Frame<Renderer>, cell: &Cell, bounds: Rectangle) {
    frame.stroke(
        &rounded(bounds, ROUND),
        canvas::Stroke {
            line_dash: canvas::LineDash {
                segments: &DASH,
                offset: 0,
            },
            ..canvas::Stroke::default()
                .with_color(look::faint())
                .with_width(OUTLINE)
        },
    );
    let title = text::cut(&cell.name, look::DETAIL, bounds.width / 2.0);
    let (at, facts) = thin_words(&title, bounds);
    text::line(
        frame,
        &title,
        at,
        look::DETAIL,
        look::muted(),
        bounds.width / 2.0,
    );
    text::line(
        frame,
        &cell.facts,
        facts,
        look::FACE,
        look::muted(),
        bounds.width / 2.0,
    );
    let (note, band) = thin_note(&cell.note, &cell.facts, facts, bounds);
    text::line(
        frame,
        &note,
        band.position(),
        look::FACE,
        noted(cell),
        bounds.width,
    );
}

/// The note of a thin row and the band it draws in: as wide as the shaper
/// sets it, with its right edge the row's own inset in from the border,
/// and cut by the shaper to the room left beside the facts, so the words
/// end inside the outline whatever the title and the note are. A note the
/// room cannot hold at all draws nothing.
pub fn thin_note(note: &str, facts: &str, at: Point, bounds: Rectangle) -> (String, Rectangle) {
    let right = bounds.x + bounds.width - INSET;
    let room = (right - at.x - text::measured(facts, look::FACE) - wall::GAP).max(0.0);
    let cut = text::measured_cut(note, look::FACE, room);
    let height = text::height(1, look::FACE);
    let (note, width) = match text::measured(&cut, look::FACE) {
        drawn if drawn <= room => (cut, drawn),
        _ => (String::new(), 0.0),
    };
    (
        note,
        area(
            right - width,
            bounds.center_y() - height / 2.0,
            width,
            height,
        ),
    )
}

// Where a thin row's title and its facts start: the title at the row's
// left inset, centered on the row, and the facts a gap right of the
// title's drawn width. The shaper measures the title, because an
// estimate drifts with the glyphs, and the facts then run into one
// title and stand far from another. The facts line's top is the title's
// top plus the difference of the two line heights, so the two share a
// baseline.
pub fn thin_words(title: &str, bounds: Rectangle) -> (Point, Point) {
    let top = bounds.center_y() - text::height(1, look::DETAIL) / 2.0;
    let at = Point::new(bounds.x + INSET, top);
    let facts = Point::new(
        at.x + text::measured(title, look::DETAIL) + wall::GAP,
        top + text::height(1, look::DETAIL) - text::height(1, look::FACE),
    );
    (at, facts)
}

// The card's ground: the entry's art decoded at a few pixels wide, scaled
// to cover the card through the linear filter, at a low opacity over
// black. The linear upscale is the blur, so the ground reads as a
// backdrop and never as pixels. The ground clips to the card and to the
// rows' own clip, because an image carries one clip, and a card half
// under the legend must not draw over it. A card with no art keeps the
// plain slot ground.
fn ground<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    cell: &Cell,
    bounds: Rectangle,
    clip: Rectangle,
) {
    let ratio = match cell.wide {
        true => still::STILL,
        false => still::POSTER,
    };
    let (width, height) = card::ground(ratio);
    let tiny = match cell.art.is_empty() {
        true => None,
        false => posters.poster(&cell.library, &cell.art, width, height),
    };
    let Some(tiny) = tiny else {
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        return;
    };
    let Some(clip) = bounds.intersection(&clip) else {
        return;
    };
    frame.fill_rectangle(bounds.position(), extent(bounds), look::BACKGROUND);
    frame.with_clip(clip, |frame| {
        for (band, handle) in tiny.bands(card::covering(bounds, ratio)) {
            frame.draw_image(
                band,
                canvas::Image::new(handle)
                    .opacity(GROUND_TONE)
                    .filter_method(FilterMethod::Linear),
            );
        }
    });
}

// The color of a note, on a card or a thin row: the accent for a title
// still to come, faint otherwise.
fn noted(cell: &Cell) -> iced_winit::core::Color {
    match cell.standing {
        Standing::Coming => look::accent(),
        _ => look::faint(),
    }
}

// The words of a card, stacked from the top of the art: the title on up
// to two lines at the name size, the year, the blurb, and the note. The
// blurb takes the lines left over the note, so the note always shows and
// the blurb is what the art's height has room for.
fn words(frame: &mut canvas::Frame<Renderer>, cell: &Cell, words: Rectangle) {
    let mut y = words.y;
    let (first, second) = wall::titled(&cell.name, words.width);
    for line in [first, second] {
        y += text::line(
            frame,
            &line,
            Point::new(words.x, y),
            look::NAME,
            look::text(),
            words.width,
        );
    }
    y += text::line(
        frame,
        &cell.facts,
        Point::new(words.x, y),
        look::CAPTION,
        look::muted(),
        words.width,
    );

    let note = match cell.note.is_empty() {
        true => 0.0,
        false => text::height(1, look::CAPTION) + LEAD,
    };
    let room = words.y + words.height - y - note - LEAD;
    let cap = (room / text::height(1, look::CAPTION)).floor().max(0.0) as usize;
    if cap > 0 {
        y += LEAD;
        // A tagline draws in the italic, as it does everywhere; the
        // plot keeps the roman face.
        let face = match cell.tagline {
            true => look::ITALIC,
            false => iced_winit::core::Font::with_name(look::FONT),
        };
        y += text::block_in(
            frame,
            &cell.blurb,
            Point::new(words.x, y),
            (look::CAPTION, face),
            look::muted(),
            words.width,
            cap,
        );
    }
    text::line(
        frame,
        &cell.note,
        Point::new(words.x, y + LEAD),
        look::CAPTION,
        noted(cell),
        words.width,
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    const FRAME: Rectangle = Rectangle {
        x: 0.0,
        y: 0.0,
        width: 1920.0,
        height: 1080.0,
    };

    #[test]
    fn the_wall_starts_under_the_band_inside_the_margins() {
        let region = region(FRAME);
        assert!(region.y > band::HEIGHT);
        assert_eq!(region.y + region.height, FRAME.height);
        assert_eq!(region.x, MARGIN);
        assert_eq!(region.width, FRAME.width - 2.0 * MARGIN);
    }

    #[test]
    fn a_thin_rows_facts_stand_a_gap_right_of_the_drawn_title_on_its_baseline() {
        let row = area(100.0, 200.0, 1500.0, wall::THIN);
        for title in ["Inhumans", "Cloak & Dagger", "Agents of S.H.I.E.L.D."] {
            let (at, facts) = thin_words(title, row);
            let drawn = text::measured(title, look::DETAIL);
            assert!(drawn > 0.0, "{title}");
            assert_eq!(facts.x, at.x + drawn + wall::GAP, "{title}");
            assert!(
                facts.x >= at.x + text::width(title, look::DETAIL) * 0.5,
                "{title}"
            );
            assert_eq!(
                facts.y + text::height(1, look::FACE),
                at.y + text::height(1, look::DETAIL),
                "{title}"
            );
        }
        let (at, _) = thin_words("Inhumans", row);
        assert_eq!(at.x, row.x + INSET);
        assert_eq!(at.y + text::height(1, look::DETAIL) / 2.0, row.center_y());
    }

    #[test]
    fn a_thin_rows_note_ends_inside_the_rows_own_border() {
        let row = area(100.0, 200.0, 1500.0, wall::THIN);
        for (name, facts) in [
            ("Inhumans", "2027"),
            ("Agents of S.H.I.E.L.D.", "2027"),
            ("A Franchise Entry Whose Name Runs On", ""),
        ] {
            let title = text::cut(name, look::DETAIL, row.width / 2.0);
            let (_, at) = thin_words(&title, row);
            let (note, band) = thin_note("Coming 15 December 2027", facts, at, row);
            assert_eq!(note, "Coming 15 December 2027", "{name}");
            assert_eq!(band.width, text::measured(&note, look::FACE), "{name}");
            assert!(band.x + band.width <= row.x + row.width - INSET, "{name}");
            assert!(band.x > at.x + text::measured(facts, look::FACE), "{name}");
            assert_eq!(band.center_y(), row.center_y(), "{name}");
        }
    }

    #[test]
    fn a_note_the_room_beside_the_title_cannot_hold_is_cut_by_the_shaper() {
        let row = area(100.0, 200.0, 250.0, wall::THIN);
        let title = text::cut("Inhumans", look::DETAIL, row.width / 2.0);
        let (_, at) = thin_words(&title, row);
        let (note, band) = thin_note("Coming 15 December 2027", "2027", at, row);
        assert!(note.ends_with('\u{2026}'), "{note}");
        assert!(band.x + band.width <= row.x + row.width - INSET);
        assert_eq!(band.width, text::measured(&note, look::FACE));
    }

    #[test]
    fn a_row_with_no_room_left_draws_no_note() {
        let row = area(100.0, 200.0, 2.0 * INSET, wall::THIN);
        let (_, at) = thin_words("A Name", row);
        let (note, band) = thin_note("Coming 15 December 2027", "2027", at, row);
        assert_eq!(note, "");
        assert_eq!(band.width, 0.0);
        assert_eq!(band.x, row.x + row.width - INSET);
    }

    #[test]
    fn a_row_outside_the_lane_builds_no_geometry() {
        let columned = wall::columned(area(100.0, 200.0, 1000.0, 800.0), true);
        assert!(!outside(
            area(columned.x, columned.y, 100.0, 100.0),
            columned
        ));
        assert!(outside(
            area(columned.x, columned.y - 300.0, 100.0, 100.0),
            columned
        ));
        assert!(outside(
            area(
                columned.x,
                columned.y + columned.height + 10.0,
                100.0,
                100.0
            ),
            columned
        ));
    }
}
