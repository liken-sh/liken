//! The tests write out the colors that `liken.css` declares. When someone
//! edits a token in that file, these tests fail until they are updated with
//! the new color.

use liken_iced::palette;
use liken_iced::palette::Palette;

/// The color as a hex string, the form `liken.css` uses.
fn hex(color: iced_widget::core::Color) -> String {
    let byte = |channel: f32| (channel * 255.0).round() as u8;

    format!(
        "#{:02x}{:02x}{:02x}",
        byte(color.r),
        byte(color.g),
        byte(color.b)
    )
}

fn tokens(palette: &Palette) -> [String; 5] {
    [
        hex(palette.ink),
        hex(palette.ink_muted),
        hex(palette.page),
        hex(palette.link),
        hex(palette.rule),
    ]
}

#[test]
fn the_light_palette_matches_the_root_block() {
    assert_eq!(
        tokens(palette::light()),
        ["#1a1a1a", "#555555", "#ffffff", "#4a5d3a", "#cccccc"]
    );
}

#[test]
fn the_dark_palette_matches_the_dark_scheme_media_query() {
    assert_eq!(
        tokens(palette::dark()),
        ["#e8e8e8", "#a0a6ad", "#16181c", "#b4c49a", "#333940"]
    );
}

#[test]
fn no_token_has_the_same_color_in_both_schemes() {
    for (light, dark) in tokens(palette::light()).iter().zip(tokens(palette::dark())) {
        assert_ne!(*light, dark);
    }
}

#[test]
fn both_link_colors_are_greens_of_the_mark() {
    // The idle screen and the media browser fill a bar with the dark
    // scheme's link color and draw its track in the light scheme's, so
    // both must stay greens of the mark.
    let greens: Vec<iced_widget::core::Color> = liken_iced::mark::hexagons()
        .iter()
        .map(|it| it.fill)
        .collect();

    assert!(greens.contains(&palette::light().link));
    assert!(greens.contains(&palette::dark().link));
}
