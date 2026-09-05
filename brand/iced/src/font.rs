//! The brand's family, Source Sans 3, as two embedded faces: the upright
//! and the italic. A program that draws the liken look loads them into
//! iced's shared font system and resolves the family out of its own binary,
//! not out of whatever a machine has installed, so every liken display
//! draws the same face.

use std::borrow::Cow;

use iced_widget::core::Font;
use iced_widget::core::font::Style;
use iced_widget::graphics::text::font_system;

/// The one family the liken look draws in.
pub const FAMILY: &str = "Source Sans 3";

/// The upright face of the family, named for the toolkit to match.
pub const REGULAR: Font = Font::with_name(FAMILY);

/// The italic face of the family, which differs from the upright one in
/// its style alone.
pub const ITALIC: Font = Font {
    style: Style::Italic,
    ..Font::with_name(FAMILY)
};

/// The bytes of the upright face, from the family's 3.052 release.
pub const REGULAR_OTF: &[u8] = include_bytes!("../../fonts/SourceSans3-Regular.otf").as_slice();

/// The bytes of the italic face, from the family's 3.052 release.
pub const ITALIC_OTF: &[u8] = include_bytes!("../../fonts/SourceSans3-It.otf").as_slice();

/// Load both faces into the shared font system every iced renderer in the
/// process shapes through. Call it before anything shapes a run of text. A
/// second call loads nothing, because the system keeps the address of a
/// borrowed file it has already read.
pub fn load() {
    let mut fonts = font_system().write().expect("the shared font system");

    fonts.load_font(Cow::Borrowed(REGULAR_OTF));
    fonts.load_font(Cow::Borrowed(ITALIC_OTF));
}
