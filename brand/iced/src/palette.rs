//! Five color tokens of `liken.css`, in its light and dark schemes.
//!
//! The stylesheet has other tokens, such as `--panel`, the background
//! of a code block, but they style parts of a web page that a canvas
//! does not have. The crate also reads `--rule`, a table border on a
//! page, because a display can use it for a line under a heading or
//! between groups of rows.
//!
//! `liken.css` is the source of the values. Its `:root` block is the
//! light scheme, and the `:root` block inside the
//! `prefers-color-scheme: dark` media query is the dark one. This
//! module parses those blocks instead of copying the values, so each
//! color is defined only in the stylesheet.
//!
//! The crate does not assign roles to tokens. Each display decides
//! which token it uses for what. `liken`'s idle screen draws text in
//! the dark scheme's `ink`, fills its bar with the dark scheme's `link`,
//! and draws the bar's track in the light scheme's `link`.

use std::sync::LazyLock;

use iced_widget::core::Color;

/// The stylesheet is compiled in, so a consumer needs no file at run
/// time.
const CSS: &str = include_str!("../../liken.css");

/// One color scheme.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Palette {
    /// `--ink`, the color of body text.
    pub ink: Color,
    /// `--ink-muted`, the color of secondary text.
    pub ink_muted: Color,
    /// `--page`, the background color.
    pub page: Color,
    /// `--link`, the accent color. It is the darkest green of the mark
    /// in the light scheme and the palest in the dark one.
    pub link: Color,
    /// `--rule`, the color of thin dividing lines, such as table
    /// borders.
    pub rule: Color,
}

static LIGHT: LazyLock<Palette> = LazyLock::new(|| Palette::read(&root_block(&stripped(), 0)));

static DARK: LazyLock<Palette> = LazyLock::new(|| {
    let css = stripped();
    let query = css
        .find("prefers-color-scheme: dark")
        .expect("liken.css has no prefers-color-scheme: dark block");

    Palette::read(&root_block(&css, query))
});

pub fn light() -> &'static Palette {
    &LIGHT
}

pub fn dark() -> &'static Palette {
    &DARK
}

impl Palette {
    /// Reads the five tokens from the declarations of one block.
    fn read(block: &str) -> Self {
        Self {
            ink: token(block, "--ink"),
            ink_muted: token(block, "--ink-muted"),
            page: token(block, "--page"),
            link: token(block, "--link"),
            rule: token(block, "--rule"),
        }
    }
}

/// The stylesheet with its comments removed.
///
/// A comment can contain `:root` or the dark media query as text, and
/// the searches below would match it.
fn stripped() -> String {
    let mut out = String::with_capacity(CSS.len());
    let mut rest = CSS;

    while let Some((before, after)) = rest.split_once("/*") {
        out.push_str(before);
        rest = after.split_once("*/").map_or("", |(_, tail)| tail);
    }
    out.push_str(rest);

    out
}

/// The declarations inside the first `:root` block at or after byte
/// offset `from`.
fn root_block(css: &str, from: usize) -> String {
    let start = from
        + css[from..]
            .find(":root")
            .expect("liken.css has no :root block");
    let open = start
        + css[start..]
            .find('{')
            .expect("a :root block has no opening brace");
    let close = open
        + css[open..]
            .find('}')
            .expect("a :root block has no closing brace");

    css[open + 1..close].to_string()
}

/// The color a custom property is declared as.
///
/// The search includes the colon, so `--ink` does not match inside
/// `--ink-muted`.
fn token(block: &str, name: &str) -> Color {
    let value = block
        .split_once(&format!("{name}:"))
        .unwrap_or_else(|| panic!("liken.css declares no {name}"))
        .1;
    let value = value
        .split_once(';')
        .unwrap_or_else(|| panic!("the declaration of {name} has no closing semicolon"))
        .0;

    hex(value.trim())
}

/// Parses a CSS or SVG hex color, in `#rgb` or `#rrggbb` form.
///
/// The mark's SVG and the stylesheet both write colors this way, so the
/// SVG parser uses this function too.
pub(crate) fn hex(text: &str) -> Color {
    let digits = text
        .strip_prefix('#')
        .unwrap_or_else(|| panic!("{text:?} is not a hex color"));
    let byte = |at: usize, width: usize| {
        let digit = &digits[at * width..at * width + width];
        let value =
            u8::from_str_radix(digit, 16).unwrap_or_else(|_| panic!("{text:?} is not a hex color"));

        // In the three-digit form each digit is doubled, so #fff is
        // #ffffff. Multiplying by 17 doubles a hex digit.
        let value = if width == 1 { value * 17 } else { value };

        f32::from(value) / 255.0
    };

    match digits.len() {
        3 => Color::from_rgb(byte(0, 1), byte(1, 1), byte(2, 1)),
        6 => Color::from_rgb(byte(0, 2), byte(1, 2), byte(2, 2)),
        _ => panic!("{text:?} is not a hex color, which has 3 or 6 digits"),
    }
}
