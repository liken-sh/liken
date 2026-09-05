// The banner is two primitives on two layers. A mesh in one layer
// draws under every image of that layer, so the scrim over the backdrop
// needs the backdrop on a layer of its own. This module holds the
// frame's geometry, the backdrop under it, and the scrim, the head, the
// facts, the genres, the scores, the tagline, the indicators, and the
// mark over it.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle};

use super::stack::Stack;
use super::{Tone, area, extent, header, layers, mark, paint, ratings, text};
use crate::look;
use crate::posters::Posters;

// The share of the page's height the frame takes.
const SHARE: f32 = 0.40;

// The inset from the frame's left edge to its text and its indicators,
// and from its top edge to its first line.
const INSET: f32 = 64.0;
const TOP: f32 = 40.0;

// The share of the frame's width the text column takes. The column
// ends inside the scrim's full shade, so every line reads over the art
// whatever the art holds.
const COLUMN: f32 = 0.42;

// The space between two blocks of text.
const GAP: f32 = 10.0;

// The box a logo draws in, at the proportions the metadata tools write
// a logo file in.
const LOGO_WIDTH: f32 = 460.0;
const LOGO_HEIGHT: f32 = 110.0;

// The lines the tagline is cut to.
const TAGLINE_LINES: usize = 2;

// One indicator's size, the gap between two, and the space under the
// row before the frame's foot.
const INDICATOR_WIDTH: f32 = 36.0;
const INDICATOR_HEIGHT: f32 = 4.0;
const INDICATOR_GAP: f32 = 10.0;
const FOOT: f32 = 28.0;

/// The height the frame takes on a page this tall.
pub fn height(page: f32) -> f32 {
    (page * SHARE).max(least()).round()
}

// The least the frame can be: the space over the head, the head, the
// facts line, the genres line, the ratings row, the tagline, the gaps
// between them, and the indicator row over the foot. Every size here is
// a fixed number of pixels, so a page under about 925 pixels tall takes
// this and not the share, and the column never runs into the indicators.
fn least() -> f32 {
    TOP + LOGO_HEIGHT
        + GAP
        + text::height(1, look::FACTS)
        + GAP
        + text::height(1, look::FACTS)
        + GAP
        + ratings::HEIGHT
        + GAP
        + text::height(TAGLINE_LINES, look::TAGLINE)
        + INDICATOR_HEIGHT
        + FOOT
}

/// The indicator of one title, inside the frame's foot.
pub fn indicator(region: Rectangle, index: usize) -> Rectangle {
    area(
        region.x + INSET + index as f32 * (INDICATOR_WIDTH + INDICATOR_GAP),
        region.y + region.height - FOOT - INDICATOR_HEIGHT,
        INDICATOR_WIDTH,
        INDICATOR_HEIGHT,
    )
}

/// The under layer: the backdrop over the frame, and the slot color
/// until it lands. The backdrop is decoded at the frame's size, so a
/// title costs one decode and never a page's.
pub fn backdrop<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    library: &str,
    art: &str,
    region: Rectangle,
) {
    match posters.poster(library, art, region.width as u32, region.height as u32) {
        Some(image) => paint(frame, &image, region, Tone::Full),
        None => frame.fill_rectangle(region.position(), extent(region), look::slot()),
    }
}

/// One banner to draw over its backdrop: the current title's words, the
/// count, the current index, the focus, and the frame.
pub struct Banner<'a> {
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The logo path, empty where the title has none.
    pub logo: &'a str,
    /// The name, drawn where the title has no logo.
    pub name: &'a str,
    /// The facts line under the head.
    pub facts: &'a str,
    /// The genres on one line, cut with an ellipsis where they run past
    /// the column.
    pub genres: &'a str,
    /// The scores the ratings row draws. An empty slice draws no row and
    /// takes no height.
    pub ratings: &'a [ratings::Score],
    /// The tagline, empty where the title has none.
    pub tagline: &'a str,
    /// How many titles the banner holds.
    pub count: usize,
    /// The index of the title the frame shows.
    pub current: usize,
    /// Whether the banner holds focus.
    pub focused: bool,
    /// The frame in the page's space.
    pub region: Rectangle,
}

/// The over layer: the scrim, the head, the facts, the tagline, the
/// indicators, and the mark while focused.
pub fn draw<P: Posters>(frame: &mut canvas::Frame<Renderer>, posters: &mut P, banner: &Banner<'_>) {
    let region = banner.region;
    layers::scrim(frame, region);

    let column = region.width * COLUMN;
    let mut stack = Stack::new(Point::new(region.x + INSET, region.y + TOP), GAP);
    let head = area(stack.at().x, stack.at().y, column, LOGO_HEIGHT);
    let taken = frame.with_clip(head, |frame| {
        header::title(
            frame,
            posters,
            &header::Title {
                library: banner.library,
                logo: banner.logo,
                name: banner.name,
                at: stack.at(),
                logo_box: (LOGO_WIDTH, LOGO_HEIGHT),
                width: column,
                size: look::HEAD_TITLE,
                lifted: false,
            },
        )
    });
    // The head takes the box's height with a logo and the text's height
    // without. The box holds only for a title with a logo path, so the
    // blocks under it stand still while the decode lands, and a title with
    // no logo path never gets one.
    stack.add(match banner.logo.is_empty() {
        true => taken.min(LOGO_HEIGHT),
        false => LOGO_HEIGHT,
    });

    let taken = text::block(
        frame,
        banner.facts,
        stack.at(),
        look::FACTS,
        look::muted(),
        column,
        1,
    );
    stack.add(taken);

    // The genres are cut to one line with an ellipsis, so a title with
    // many of them never pushes the blocks under it down.
    let taken = text::block(
        frame,
        &text::cut(banner.genres, look::FACTS, column),
        stack.at(),
        look::FACTS,
        look::muted(),
        column,
        1,
    );
    stack.add(taken);

    let taken = ratings::draw(frame, banner.ratings, stack.at());
    stack.add(taken);

    // A tagline is the film's own words, so it draws in the italic, as
    // a card's tagline does.
    text::block_in(
        frame,
        banner.tagline,
        stack.at(),
        (look::TAGLINE, look::ITALIC),
        look::text(),
        column,
        TAGLINE_LINES,
    );

    for index in 0..banner.count {
        let bar = indicator(region, index);
        let color = match index == banner.current {
            true => look::text(),
            false => look::faint(),
        };
        frame.fill_rectangle(bar.position(), extent(bar), color);
    }

    if banner.focused {
        mark(frame, region);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn region() -> Rectangle {
        area(32.0, 104.0, 1856.0, height(1080.0))
    }

    #[test]
    fn the_frame_takes_about_four_tenths_of_the_page() {
        assert_eq!(height(1080.0), 432.0);
    }

    #[test]
    fn a_short_page_gives_the_frame_the_height_its_text_needs() {
        assert_eq!(height(720.0), 370.0);
        assert!(height(720.0) > 720.0 * SHARE);
    }

    #[test]
    fn the_indicators_stand_in_a_row_inside_the_frames_foot() {
        let region = region();
        let first = indicator(region, 0);
        let second = indicator(region, 1);
        assert_eq!(first.x, region.x + INSET);
        assert_eq!(second.x, first.x + INDICATOR_WIDTH + INDICATOR_GAP);
        assert_eq!(first.y, second.y);
        assert!(first.y + first.height < region.y + region.height);
        assert!(first.y > region.y + region.height / 2.0);
    }

    // The tallest the text column gets: the head, the facts line, the
    // genres line, the ratings row, the tagline, and a gap between each
    // two of them.
    fn column() -> f32 {
        TOP + LOGO_HEIGHT
            + GAP
            + text::height(1, look::FACTS)
            + GAP
            + text::height(1, look::FACTS)
            + GAP
            + ratings::HEIGHT
            + GAP
            + text::height(TAGLINE_LINES, look::TAGLINE)
    }

    // The room the frame leaves between the foot of the text column and
    // the indicator row on a page this tall.
    fn room(page: f32) -> f32 {
        let region = area(32.0, 104.0, 1856.0, height(page));
        indicator(region, 0).y - (region.y + column())
    }

    #[test]
    fn the_text_and_the_indicators_stay_clear_of_each_other() {
        assert!(room(1080.0) >= 0.0, "at 1080p: {}", room(1080.0));
        assert!(room(720.0) >= 0.0, "at 720p: {}", room(720.0));
    }
}
