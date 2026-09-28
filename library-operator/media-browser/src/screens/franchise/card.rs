// One held entry of the franchise wall as a card the whole width of the
// lane. A card draws three layers: a ground made from the entry's own
// art, the sharp 16:9 art at the left at the cap, and the words beside
// it: the title, the year, the tagline or the first lines of the plot,
// and the note. The ground is the art decoded again at about 24 pixels
// wide and drawn to cover the card with linear filtering at a low
// opacity over black. The linear upscale of a tiny image is the blur, so
// the browser needs no blur crate.

use iced_winit::core::Rectangle;

use super::wall::{GAP, poster_width};
use crate::views::{area, wall};

/// The width the ground decodes at; the height follows the art's ratio.
/// A decode this small costs under a kilobyte in the cache and takes the
/// poster lane.
pub const GROUND: u32 = 24;

/// The opacity the ground draws at over the black card, so the art behind
/// the words reads as a dark backdrop and never fights the sharp art. It
/// is the image's own opacity and not a veil over it, because a fill
/// draws under every image of its layer. The surface blends in linear
/// light, so 0.18 reads as about 46% brightness on a white backdrop and
/// about 21% on a mid-grey one; 0.4 left a white backdrop light enough
/// to hide a title.
pub const GROUND_TONE: f32 = 0.18;

/// The box a card's 16:9 art draws in: a gap in from the card's left and
/// top, at the height the wall measured.
pub fn art_box(card: Rectangle, art: f32) -> Rectangle {
    area(card.x + GAP, card.y + GAP, art / wall::STILL, art)
}

/// The box a card's poster draws in when the item holds no landscape art:
/// the poster's own ratio at the art's height, at the left of the art
/// box, so the words start beside the poster.
pub fn poster_box(art: Rectangle) -> Rectangle {
    area(art.x, art.y, poster_width(art.height), art.height)
}

/// The box a card's words draw in: from a gap right of the art to a gap
/// short of the card's edge, as tall as the art.
pub fn words_box(card: Rectangle, art: Rectangle) -> Rectangle {
    let x = art.x + art.width + GAP;
    area(
        x,
        art.y,
        (card.x + card.width - GAP - x).max(0.0),
        art.height,
    )
}

/// The pixel size the ground decodes at for art of this ratio, height
/// over width.
pub fn ground(ratio: f32) -> (u32, u32) {
    (GROUND, (GROUND as f32 * ratio).round().max(1.0) as u32)
}

/// The box the ground draws into: art of this ratio scaled to cover the
/// card, centered on it. The card clips it, so the overflow past the
/// card's edge never shows.
pub fn covering(card: Rectangle, ratio: f32) -> Rectangle {
    let (width, height) = match card.height / card.width > ratio {
        true => (card.height / ratio, card.height),
        false => (card.width, card.width * ratio),
    };
    area(
        card.center_x() - width / 2.0,
        card.center_y() - height / 2.0,
        width,
        height,
    )
}

#[cfg(test)]
mod tests;
