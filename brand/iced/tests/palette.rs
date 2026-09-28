//! The palette comes out of `liken.css`. The values below are the ones that
//! file declares, so an edit to a token fails here until the test states the
//! new color.

use liken_iced::palette;
use liken_iced::palette::Palette;

/// A color the way `liken.css` writes it, as a hex string.
fn hex(color: iced_widget::core::Color) -> String {
    let byte = |channel: f32| (channel * 255.0).round() as u8;

    format!(
        "#{:02x}{:02x}{:02x}",
        byte(color.r),
        byte(color.g),
        byte(color.b)
    )
}

fn tokens(palette: &Palette) -> [String; 4] {
    [
        hex(palette.ink),
        hex(palette.ink_muted),
        hex(palette.page),
        hex(palette.link),
    ]
}

#[test]
fn the_light_scheme_is_the_root_block() {
    assert_eq!(
        tokens(palette::light()),
        ["#1a1a1a", "#555555", "#ffffff", "#4a5d3a"]
    );
}

#[test]
fn the_dark_scheme_is_the_block_under_the_query() {
    assert_eq!(
        tokens(palette::dark()),
        ["#e8e8e8", "#a0a6ad", "#16181c", "#b4c49a"]
    );
}

#[test]
fn no_token_carries_the_same_color_in_both_schemes() {
    for (light, dark) in tokens(palette::light()).iter().zip(tokens(palette::dark())) {
        assert_ne!(*light, dark);
    }
}

#[test]
fn the_accent_of_one_scheme_is_the_mark_of_the_other() {
    // The two links are the darkest and the palest of the mark's greens, so a
    // screen that reads the light link over the dark link draws one color
    // family. `liken`'s idle screen fills with the dark link and tracks with
    // the light one.
    let greens: Vec<iced_widget::core::Color> = liken_iced::mark::hexagons()
        .iter()
        .map(|it| it.fill)
        .collect();

    assert!(greens.contains(&palette::light().link));
    assert!(greens.contains(&palette::dark().link));
}
