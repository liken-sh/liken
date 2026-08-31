//! Four of the six color tokens of `liken.css`, in both of its schemes. The
//! other two, `--panel` and `--rule`, are the fill behind a code block and the
//! border of a table, which a page has and a canvas does not.
//!
//! `liken.css` is the original. Its `:root` block holds the light scheme, and
//! the block under the `prefers-color-scheme: dark` query holds the dark one. A
//! program that draws with `iced` reads the same values a page reads, so a
//! color changes in the stylesheet and nowhere else.
//!
//! The crate gives a token no role. A display names the roles it needs, and two
//! displays name them differently. `liken`'s idle screen draws its text in the
//! dark `ink` and its accent in the dark `link`.

use std::sync::LazyLock;

use iced_widget::core::Color;

/// The stylesheet, embedded so a consumer needs no file at run time.
const CSS: &str = include_str!("../../liken.css");

/// One color scheme.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Palette {
    /// `--ink`, the color of body text.
    pub ink: Color,
    /// `--ink-muted`, the color of text that reads under body text.
    pub ink_muted: Color,
    /// `--page`, the color of the ground behind everything.
    pub page: Color,
    /// `--link`, the accent. It is the darkest lichen green on a light page
    /// and the palest on a dark one, so the accent is one color family in both
    /// schemes.
    pub link: Color,
}

static LIGHT: LazyLock<Palette> = LazyLock::new(|| Palette::read(&root_block(&stripped(), 0)));

static DARK: LazyLock<Palette> = LazyLock::new(|| {
    let css = stripped();
    let query = css
        .find("prefers-color-scheme: dark")
        .expect("liken.css declares no dark scheme");

    Palette::read(&root_block(&css, query))
});

/// The scheme a reader with a light system setting gets.
pub fn light() -> &'static Palette {
    &LIGHT
}

/// The scheme a reader with a dark system setting gets.
pub fn dark() -> &'static Palette {
    &DARK
}

impl Palette {
    /// The four tokens of one block of declarations.
    fn read(block: &str) -> Self {
        Self {
            ink: token(block, "--ink"),
            ink_muted: token(block, "--ink-muted"),
            page: token(block, "--page"),
            link: token(block, "--link"),
        }
    }
}

/// The stylesheet with its comments removed.
///
/// A comment can hold the text this module searches for, `:root` or the dark
/// query, and a comment declares nothing. Removing the comments first keeps the
/// search on the rules.
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

/// The declarations inside the first `:root` block at or after `from`.
fn root_block(css: &str, from: usize) -> String {
    let start = from
        + css[from..]
            .find(":root")
            .expect("liken.css has no :root block");
    let open = start
        + css[start..]
            .find('{')
            .expect("a :root block opens no braces");
    let close = open
        + css[open..]
            .find('}')
            .expect("a :root block closes no braces");

    css[open + 1..close].to_string()
}

/// The color one custom property declares.
///
/// The name carries its colon, so the search for `--ink` does not match the
/// declaration of `--ink-muted`.
fn token(block: &str, name: &str) -> Color {
    let value = block
        .split_once(&format!("{name}:"))
        .unwrap_or_else(|| panic!("liken.css declares no {name}"))
        .1;
    let value = value
        .split_once(';')
        .unwrap_or_else(|| panic!("the declaration of {name} ends in no semicolon"))
        .0;

    hex(value.trim())
}

/// A CSS or SVG hex color, in the three-digit form or the six-digit one.
///
/// The mark and the stylesheet both write their colors this way, so the SVG
/// parser reads them through this function too.
pub(crate) fn hex(text: &str) -> Color {
    let digits = text
        .strip_prefix('#')
        .unwrap_or_else(|| panic!("{text} is no hex color"));
    let byte = |at: usize, width: usize| {
        let digit = &digits[at * width..at * width + width];
        let value =
            u8::from_str_radix(digit, 16).unwrap_or_else(|_| panic!("{text} is no hex color"));

        // The three-digit form repeats each digit, so #fff is #ffffff.
        let value = if width == 1 { value * 17 } else { value };

        f32::from(value) / 255.0
    };

    match digits.len() {
        3 => Color::from_rgb(byte(0, 1), byte(1, 1), byte(2, 1)),
        6 => Color::from_rgb(byte(0, 2), byte(1, 2), byte(2, 2)),
        _ => panic!("{text} is no hex color"),
    }
}
