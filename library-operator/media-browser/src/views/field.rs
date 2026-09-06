// The text a person is typing, and the box it draws in. The field holds
// the text and nothing else, so the keyboard grid and a physical
// keyboard press into it by the same word.
//
// The field draws in the strip, leftward from the clock, and the search
// icon draws inside its left end. The band never draws it.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle};

use super::clock::{self, strip};
use super::{area, band, extent, rounded, text};
use crate::look;

// The word for the key that removes the last character.
const BACKSPACE: &str = "backspace";

/// The text a person typed. `press` is the one way it changes.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TextField {
    text: String,
}

impl TextField {
    /// A field holding this text, which a search wall opened by a letter
    /// starts with.
    pub fn of(text: &str) -> Self {
        Self {
            text: text.to_string(),
        }
    }

    /// The text as typed so far.
    pub fn text(&self) -> &str {
        &self.text
    }

    /// Fold one browser word in. A lowercase letter, a digit, or a space
    /// is appended. Backspace removes the last character. Every other
    /// word changes nothing. The answer is whether the text changed, so
    /// the caller rereads only on a change.
    pub fn press(&mut self, word: &str) -> bool {
        if word == BACKSPACE {
            return self.text.pop().is_some();
        }
        let Some(letter) = one(word) else {
            return false;
        };
        if letter != ' ' && !letter.is_ascii_lowercase() && !letter.is_ascii_digit() {
            return false;
        }
        self.text.push(letter);
        true
    }
}

/// Whether the word is one letter or one digit. The browser opens a
/// search on such a word. The space and backspace are not included,
/// because neither starts a search.
pub fn typed(word: &str) -> bool {
    one(word).is_some_and(|letter| letter.is_ascii_lowercase() || letter.is_ascii_digit())
}

/// Whether the word edits a field's text: a letter, a digit, or the
/// space. Backspace is not included, because the browser routes it
/// through the escape path before a field sees it.
pub fn edits(word: &str) -> bool {
    typed(word) || word == " "
}

// The one character a word is, or nothing where the word is a name.
fn one(word: &str) -> Option<char> {
    let mut letters = word.chars();
    let letter = letters.next()?;
    letters.next().is_none().then_some(letter)
}

// The margin at both ends of the text inside the box, the width of the
// cursor's bar, and the radius the box rounds by.
const PAD: f32 = 16.0;
const CURSOR: f32 = 3.0;
const RADIUS: f32 = 8.0;

// The typed text draws at the size of a band's heading, because the
// typing is what the wall under it is about.
const SIZE: f32 = look::NAME;

// The height of the field's box, and the share of the frame it takes
// across.
const BOX: f32 = 48.0;
const SHARE: f32 = 3.0;

/// The field's box: its right edge a margin to the left of the clock,
/// and its width about a third of the frame.
pub fn bounds(width: f32) -> Rectangle {
    let across = width / SHARE;
    area(
        clock::left(width) - band::PAD - across,
        (band::HEIGHT - BOX) / 2.0,
        across,
        BOX,
    )
}

/// The search icon's box inside the field's left end, where the icon
/// draws once the field has expanded from it.
pub fn icon(bounds: Rectangle) -> Rectangle {
    area(
        bounds.x + PAD,
        bounds.y + (bounds.height - strip::GLASS) / 2.0,
        strip::GLASS,
        strip::GLASS,
    )
}

// Where the text starts inside the box: past the icon at the left end,
// with the same margin on both sides of it.
fn lead() -> f32 {
    PAD + strip::GLASS + PAD
}

/// The cursor's bar, after the shaped text. Text wider than the box
/// holds the bar at the right margin, so the bar never leaves the box.
pub fn cursor(bounds: Rectangle, text: &str) -> Rectangle {
    let after = (bounds.x + lead() + text::measured(text, SIZE)).min(bounds.x + bounds.width - PAD);
    area(after, bounds.y + (bounds.height - SIZE) / 2.0, CURSOR, SIZE)
}

/// Draw the field in the strip of a frame this wide.
pub fn draw(frame: &mut canvas::Frame<Renderer>, width: f32, field: &TextField) {
    let bounds = bounds(width);
    frame.fill(&rounded(bounds, RADIUS), look::slot());
    let bar = cursor(bounds, field.text());
    frame.fill_rectangle(bar.position(), extent(bar), look::mark());
    text::line(
        frame,
        field.text(),
        Point::new(bounds.x + lead(), bar.y),
        SIZE,
        look::text(),
        bounds.width - lead() - PAD,
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    // One case: the text the field held, the word pressed, whether the
    // text changed, and the text after.
    const PRESSES: [(&str, &str, bool, &str); 12] = [
        ("", "a", true, "a"),
        ("bat", "m", true, "batm"),
        ("bat", "7", true, "bat7"),
        ("bat", " ", true, "bat "),
        ("bat", "backspace", true, "ba"),
        ("b", "backspace", true, ""),
        ("", "backspace", false, ""),
        ("bat", "up", false, "bat"),
        ("bat", "enter", false, "bat"),
        ("bat", "A", false, "bat"),
        ("bat", "·", false, "bat"),
        ("bat", "", false, "bat"),
    ];

    #[test]
    fn a_press_appends_a_character_takes_one_back_or_changes_nothing() {
        for (held, word, changed, after) in PRESSES {
            let mut field = TextField::of(held);
            assert_eq!(field.press(word), changed, "{held:?} {word:?}");
            assert_eq!(field.text(), after, "{held:?} {word:?}");
        }
    }

    #[test]
    fn a_new_field_holds_nothing() {
        assert_eq!(TextField::default().text(), "");
    }

    // One case: the word, whether it opens a search, and whether it edits
    // a field.
    const WORDS: [(&str, bool, bool); 8] = [
        ("a", true, true),
        ("7", true, true),
        (" ", false, true),
        ("backspace", false, false),
        ("enter", false, false),
        ("up", false, false),
        ("A", false, false),
        ("", false, false),
    ];

    #[test]
    fn a_letter_and_a_digit_open_a_search_and_the_space_only_edits() {
        for (word, opens, edits_it) in WORDS {
            assert_eq!(typed(word), opens, "{word:?}");
            assert_eq!(edits(word), edits_it, "{word:?}");
        }
    }

    #[test]
    fn the_field_ends_a_margin_to_the_left_of_the_clock_and_takes_a_third_across() {
        let bounds = bounds(1920.0);
        assert_eq!(bounds.x + bounds.width, clock::left(1920.0) - band::PAD);
        assert_eq!(bounds.width, 1920.0 / SHARE);
        assert!(bounds.y > 0.0);
        assert!(bounds.y + bounds.height < band::HEIGHT);
    }

    #[test]
    fn the_icon_sits_at_the_left_end_and_the_text_starts_past_it() {
        let bounds = bounds(1920.0);
        let icon = icon(bounds);
        assert_eq!(icon.x, bounds.x + PAD);
        assert!(icon.x + icon.width < bounds.x + lead());
        assert_eq!(icon.center_y(), bounds.center_y());
    }

    #[test]
    fn the_cursor_follows_the_text_and_stays_inside_the_box() {
        let bounds = bounds(1920.0);
        let empty = cursor(bounds, "");
        assert_eq!(empty.x, bounds.x + lead());
        assert!(empty.y > bounds.y);
        assert!(empty.y + empty.height < bounds.y + bounds.height);

        let typed = cursor(bounds, "batman");
        assert!(typed.x > empty.x);

        let long = cursor(bounds, &"w".repeat(200));
        assert_eq!(long.x, bounds.x + bounds.width - PAD);
    }
}
