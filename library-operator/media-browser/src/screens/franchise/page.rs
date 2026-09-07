// The franchise page's two canvases: the wall, with the time labels at
// the left, the metro strip and one lane of cards beside them, and the
// era headings in the flow of the rows; and over it the one line held
// at the top of the cards while the wall is inside an era, on a ground
// of its own. The second canvas is a layer of its own, the way the band
// over the page is, because inside one canvas the renderer draws every
// fill before any text, and a ground drawn in the wall's canvas would
// draw under the words of rows that scroll up under the line. The wall
// scrolls inside its own region and is clipped to it, and the rows are
// clipped under the held line, so no art draws over it. A row the region does not reach builds no geometry, so
// a wall of a hundred rows costs only the rows a person sees.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::image::FilterMethod;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::Franchise;
use super::card::{self, GROUND_TONE};
use super::metro;
use super::wall::{self, Cell, GAP};
use crate::art::Art;
use crate::catalog::franchise::Standing;
use crate::look;
use crate::views::{
    REACH, Tone, area, artwork, band, divider, extent, mark, rounded, text, wall as still,
};

// The margin at both sides of the page.
const MARGIN: f32 = 80.0;

// The space between the band and the first row.
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
pub struct Page<'a, A> {
    /// The franchise the page is about.
    pub franchise: &'a Franchise,
    /// The store the entries' art comes from.
    pub store: &'a RefCell<A>,
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
        let page = self.franchise;
        // The page's focus while the page holds it, and none while the
        // browser's strip does, so one mark draws on the glass. The scroll
        // still follows the page's own focus, so the strip never moves the
        // wall under it.
        let focus = self.held.then_some(page.focus);
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let store = &mut *self.store.borrow_mut();

        let rows = &page.rows;
        let Layout {
            region,
            wall,
            columned,
            strip,
            cards,
            art,
            tops,
            down,
            headings,
            held,
        } = layout(page, bounds);

        frame.with_clip(region, |frame| {
            times(
                frame,
                page,
                wall,
                &tops,
                down,
                column(wall, page.time, region),
            );

            // The strip is clipped to its own part of the region, so a
            // row that has scrolled up draws nothing over the time labels
            // beside it, and the rows are clipped under the held line.
            let cells = wall::clipped(columned);
            frame.with_clip(cells, |frame| {
                metro::draw(frame, strip, &page.runs, rows, &page.headings, &tops, down);
            });
            let cells = wall::clipped(wall::under(cards, held.is_some()));
            frame.with_clip(cells, |frame| {
                for (index, row) in rows.iter().enumerate() {
                    let bounds = wall::cell_box(cards, index, &page.headings, &tops, down);
                    if outside(bounds, cells) {
                        continue;
                    }
                    match row.cell.held() {
                        true => entry(frame, store, &row.cell, bounds, cells, art),
                        false => thin(frame, &row.cell, bounds),
                    }
                    if focus == Some(index) {
                        mark(frame, bounds);
                    }
                }
            });

            // The headings draw in the flow of the rows, and scroll up
            // under the held line with them.
            for (heading, bounds) in page.headings.iter().zip(&headings) {
                if outside(*bounds, region) {
                    continue;
                }
                let words = heading.label();
                match heading.depth {
                    0 => divider::two(frame, *bounds, &words, wall::bright(&words)),
                    _ => subheading(frame, *bounds, &words),
                }
            }
        });

        vec![frame.into_geometry()]
    }
}

/// The layer over the wall: the one line held at the top while the wall
/// is inside an era, on an opaque ground, so it stands over the rows
/// under it.
pub struct Held<'a> {
    /// The franchise the page is about.
    pub franchise: &'a Franchise,
}

impl canvas::Program<Infallible, Theme, Renderer> for Held<'_> {
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
        let Layout {
            region,
            cards,
            held,
            ..
        } = layout(page, bounds);
        // The space between the band over the page and the wall is
        // painted here, over the wall, because the renderer clips a line
        // of text as a whole and a row's words that cross the top of the
        // wall would show in it.
        let gap = area(region.x, band::HEIGHT, region.width, TOP);
        frame.fill_rectangle(gap.position(), extent(gap), look::BACKGROUND);
        // The line stands in the cards' column, where the headings in
        // the flow stand, so it reads as one of them held still, and the
        // strip and the time labels beside it run on up to the top. Its
        // ground reaches the focus mark's room to the left, so the mark
        // of a row under it is covered whole. The ground takes a clip of
        // its own, and never the wall's region, because the renderer
        // folds a clip with the bounds of an earlier one into that
        // earlier layer, and the ground would then draw under the rows'
        // words.
        if let Some(line) = &held {
            let band = wall::band(cards, true);
            let ground = area(band.x - REACH, band.y, band.width + REACH, band.height);
            frame.with_clip(ground, |frame| {
                frame.fill_rectangle(ground.position(), extent(ground), look::BACKGROUND);
                divider::two(frame, band, line, wall::bright(line));
            });
        }
        vec![frame.into_geometry()]
    }
}

/// The measures both canvases draw from, so the two agree on every
/// frame.
struct Layout {
    region: Rectangle,
    wall: Rectangle,
    columned: Rectangle,
    strip: Rectangle,
    cards: Rectangle,
    art: f32,
    tops: Vec<f32>,
    down: f32,
    headings: Vec<Rectangle>,
    held: Option<String>,
}

// The layout of the page in this frame: the lane measures the time
// column, the strip, and the cards, and centers the cards where nothing
// stands at the left; the tops lay the rows and their headings out; the
// scroll follows focus; and the headings stand where the scroll puts
// them.
fn layout(page: &Franchise, bounds: Rectangle) -> Layout {
    let region = region(bounds);
    let wall::Lane {
        wall,
        columned,
        strip,
        cards,
    } = wall::Lane::of(region, &page.runs, page.time);
    let art = wall::art_height(region.height - wall::HEAD);
    let tops = wall::tops(&page.rows, &page.headings, art, wall::HEAD);
    // Both canvases read the scroll the last frame left and write the
    // one they draw with, and the second finds the first's answer
    // already settled, so the two agree.
    let down = wall::scroll(
        page.scrolled.get(),
        page.focus,
        &page.headings,
        &tops,
        region.height,
    );
    page.scrolled.set(down);
    let headings = wall::heading_boxes(cards, &page.headings, &tops, down);
    let held = wall::crumb(&page.headings, &tops, down);
    Layout {
        region,
        wall,
        columned,
        strip,
        cards,
        art,
        tops,
        down,
        headings,
        held,
    }
}

// One sub-heading: the words of an era that starts on the same row as a
// wider one, at the caption size and with no rule, so it reads as part
// of the heading over it. The name is muted and the count a step
// fainter, the way a heading's name is bright and its count muted.
fn subheading(frame: &mut canvas::Frame<Renderer>, bounds: Rectangle, words: &str) {
    let at = Point::new(
        bounds.x,
        bounds.y + bounds.height - divider::LIFT - text::height(1, look::CAPTION),
    );
    for (content, color) in [(words, look::faint()), (wall::bright(words), look::muted())] {
        text::line(frame, content, at, look::CAPTION, color, bounds.width);
    }
}

// Whether a row falls above or below the part of the frame the lane
// draws in, which then builds no geometry for it.
fn outside(cell: Rectangle, columned: Rectangle) -> bool {
    cell.y + cell.height < columned.y || cell.y > columned.y + columned.height
}

// Every row's time label, in the column. A label the column does not
// reach builds no geometry, and a label the row above
// carries too draws nothing, so a run of rows in one year prints the
// year once.
fn times(
    frame: &mut canvas::Frame<Renderer>,
    page: &Franchise,
    wall: Rectangle,
    tops: &[f32],
    down: f32,
    column: Rectangle,
) {
    frame.with_clip(column, |frame| {
        for index in 0..page.rows.len() {
            let label = wall::time_box(wall, page.time, index, &page.headings, tops, down);
            if label.y + label.height < column.y || label.y > column.y + column.height {
                continue;
            }
            let (first, second) = wall::stacked(wall::label_at(&page.rows, index));
            stacked(frame, &[first, second], label.position(), look::muted());
        }
    });
}

// A stack of lines at the caption size, one line to a line. Every line
// was measured against the column's own width before it reached here, so
// each draws unbounded and takes one line's height. A width would let
// the shaper break a word the column cannot hold across two lines, and
// the second of them would draw over the line under it.
fn stacked(
    frame: &mut canvas::Frame<Renderer>,
    lines: &[String],
    at: Point,
    color: iced_winit::core::Color,
) {
    let mut y = at.y;
    for line in lines {
        text::line(
            frame,
            line,
            Point::new(at.x, y),
            look::CAPTION,
            color,
            f32::INFINITY,
        );
        y += text::height(1, look::CAPTION);
    }
}

// The part of the region the times draw in: the time column, the
// whole height of the region.
fn column(wall: Rectangle, time: f32, region: Rectangle) -> Rectangle {
    area(wall.x, region.y, (time - GAP).max(0.0), region.height)
}

// One held entry of the order as a card of three layers: the ground made
// from its own art, the sharp art at the left, and the words beside the
// art. The ground draws first, then the art, then the words; the images
// of one layer draw in the order the canvas drew them, so the sharp art
// lands over the ground.
fn entry<A: Art>(
    frame: &mut canvas::Frame<Renderer>,
    store: &mut A,
    cell: &Cell,
    bounds: Rectangle,
    clip: Rectangle,
    art: f32,
) {
    ground(frame, store, cell, bounds, clip);
    let box_of = card::art_box(bounds, art);
    let art = match cell.wide {
        true => box_of,
        false => card::poster_box(box_of),
    };
    artwork(frame, store, &cell.library, &cell.art, art, "", Tone::Full);
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
// rows' own clip, because an image carries one clip, and a card that has
// scrolled past the top of the lane must not draw over what is above it.
// A card with no art keeps the plain slot ground.
fn ground<A: Art>(
    frame: &mut canvas::Frame<Renderer>,
    store: &mut A,
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
        false => store.covered(&cell.library, &cell.art, width, height),
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
        let columned = wall::columned(area(100.0, 200.0, 1000.0, 800.0), 120.0);
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
