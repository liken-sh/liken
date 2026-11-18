// Reads the embedded fonts with the font database that `iced` uses.

use iced_widget::core::font::{Family, Style};
use iced_widget::graphics::text::cosmic_text::fontdb;
use iced_widget::graphics::text::font_system;
use liken_iced::font;

// The single face that a font file contains.
fn face(bytes: &'static [u8]) -> fontdb::FaceInfo {
    let mut database = fontdb::Database::new();
    database.load_font_data(bytes.to_vec());

    database
        .faces()
        .next()
        .expect("the embedded file holds no face")
        .clone()
}

fn families(info: &fontdb::FaceInfo) -> Vec<String> {
    info.families.iter().map(|(name, _)| name.clone()).collect()
}

#[test]
fn the_embedded_regular_file_is_the_family_in_the_normal_style() {
    let info = face(font::REGULAR_OTF);

    assert!(families(&info).contains(&font::FAMILY.to_string()));
    assert_eq!(info.style, fontdb::Style::Normal);
}

#[test]
fn the_embedded_italic_file_is_the_family_in_the_italic_style() {
    let info = face(font::ITALIC_OTF);

    assert!(families(&info).contains(&font::FAMILY.to_string()));
    assert_eq!(info.style, fontdb::Style::Italic);
}

#[test]
fn load_adds_both_styles_to_the_shared_font_system() {
    font::load();

    let mut fonts = font_system()
        .write()
        .expect("locking the shared font system for writing");
    let styles: Vec<fontdb::Style> = fonts
        .raw()
        .db()
        .faces()
        .filter(|info| info.families.iter().any(|(name, _)| name == font::FAMILY))
        .map(|info| info.style)
        .collect();

    assert!(styles.contains(&fontdb::Style::Normal));
    assert!(styles.contains(&fontdb::Style::Italic));
}

#[test]
fn the_regular_and_italic_fonts_share_a_family_and_differ_in_style() {
    assert_eq!(font::REGULAR.family, Family::Name(font::FAMILY));
    assert_eq!(font::ITALIC.family, Family::Name(font::FAMILY));
    assert_eq!(font::REGULAR.style, Style::Normal);
    assert_eq!(font::ITALIC.style, Style::Italic);
    assert_eq!(font::ITALIC.weight, font::REGULAR.weight);
    assert_eq!(font::ITALIC.stretch, font::REGULAR.stretch);
}
