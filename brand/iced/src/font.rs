//! The brand's font family, Source Sans 3, embedded in the binary as an
//! upright face and an italic face. A program loads them into the shared
//! font system of `iced` and does not depend on the fonts installed on the
//! machine, so every `liken` display draws the same typeface.

use std::borrow::Cow;

use iced_widget::core::Font;
use iced_widget::core::font::Style;
use iced_widget::graphics::text::font_system;

/// The name of the font family that the `liken` look uses.
pub const FAMILY: &str = "Source Sans 3";

/// The upright face, selected by family name.
pub const REGULAR: Font = Font::with_name(FAMILY);

/// The italic face.
pub const ITALIC: Font = Font {
    style: Style::Italic,
    ..Font::with_name(FAMILY)
};

/// The font file of the upright face, from the 3.052 release.
pub const REGULAR_OTF: &[u8] = include_bytes!("../../fonts/SourceSans3-Regular.otf").as_slice();

/// The font file of the italic face, from the 3.052 release.
pub const ITALIC_OTF: &[u8] = include_bytes!("../../fonts/SourceSans3-It.otf").as_slice();

/// Loads both faces into the font system that every `iced` renderer in the
/// process shares. Call it before any text is shaped. A second call loads
/// nothing, because `iced` skips a borrowed slice whose address it has
/// already loaded.
pub fn load() {
    let mut fonts = font_system()
        .write()
        .expect("locking the shared font system for writing");

    fonts.load_font(Cow::Borrowed(REGULAR_OTF));
    fonts.load_font(Cow::Borrowed(ITALIC_OTF));
}
