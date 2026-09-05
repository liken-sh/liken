// The card's measures over invented boxes: where the art and the words
// stand, and what the ground covers.

use super::*;
use crate::screens::franchise::wall::card_height;

#[test]
fn the_art_stands_at_the_left_of_the_card_a_gap_in_from_its_edges() {
    let card = area(100.0, 200.0, 900.0, card_height(150.0));
    let art = art_box(card, 150.0);
    assert_eq!((art.x, art.y), (card.x + GAP, card.y + GAP));
    assert_eq!(art.height, 150.0);
    assert_eq!(art.height / art.width, wall::STILL);
    assert_eq!(art.y + art.height + GAP, card.y + card.height);
}

#[test]
fn a_poster_stands_at_the_left_of_the_art_box_at_its_own_ratio() {
    let art = area(100.0, 200.0, 150.0 / wall::STILL, 150.0);
    let poster = poster_box(art);
    assert_eq!((poster.x, poster.y), (art.x, art.y));
    assert_eq!(poster.width, poster_width(150.0));
    assert_eq!(poster.height, 150.0);
    assert!(poster.width < art.width);
}

#[test]
fn the_words_stand_beside_the_art_to_the_cards_edge() {
    let card = area(100.0, 200.0, 900.0, card_height(150.0));
    let art = art_box(card, 150.0);
    let words = words_box(card, art);
    assert_eq!(words.x, art.x + art.width + GAP);
    assert_eq!(words.y, art.y);
    assert_eq!(words.height, art.height);
    assert_eq!(words.x + words.width, card.x + card.width - GAP);
    assert_eq!(words_box(area(0.0, 0.0, 10.0, 10.0), art).width, 0.0);
}

#[test]
fn the_ground_is_a_tiny_decode_at_the_arts_own_ratio() {
    let (width, height) = ground(wall::STILL);
    assert_eq!((width, height), (GROUND, 14));
    assert!(width * height * 4 < 4096);
    assert_eq!(ground(wall::POSTER), (GROUND, 36));
}

#[test]
fn the_ground_covers_the_card_at_its_ratio_and_centers_on_it() {
    let wide = area(0.0, 0.0, 1000.0, 300.0);
    let covered = covering(wide, wall::STILL);
    assert_eq!(covered.width, 1000.0);
    assert_eq!(covered.height, 562.5);
    assert_eq!(covered.center_y(), wide.center_y());
    assert_eq!(covered.x, wide.x);

    let tall = area(0.0, 0.0, 200.0, 300.0);
    let covered = covering(tall, wall::STILL);
    assert_eq!(covered.height, 300.0);
    assert!(covered.width > tall.width);
    assert_eq!(covered.center_x(), tall.center_x());
}
