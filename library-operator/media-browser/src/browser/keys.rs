// The table that turns a kernel key name into the word the screens
// take. Letters and digits come through it too: a remote with a
// keyboard, like the Fire TV X6, sends them as ordinary evdev keys, and
// the search wall types them.

/// One kernel key name as the browser key it is. Several names reach one
/// key, because remotes differ in the name they send for OK and for back.
/// Select is enter and back is escape, so a press from a remote takes the
/// path the keyboard and the script take. Home pops to the home page,
/// search opens the search wall, and a letter or a digit is the
/// character itself, which is the word a typed key gives on a local run.
pub fn key_of(name: &str) -> Option<&'static str> {
    match name {
        "KEY_UP" => Some("up"),
        "KEY_DOWN" => Some("down"),
        "KEY_LEFT" => Some("left"),
        "KEY_RIGHT" => Some("right"),
        "KEY_ENTER" | "KEY_OK" | "KEY_SELECT" | "KEY_KPENTER" => Some("enter"),
        "KEY_BACK" | "KEY_ESC" | "KEY_EXIT" => Some("escape"),
        "KEY_HOMEPAGE" => Some("home"),
        "KEY_SEARCH" => Some("search"),
        "KEY_BACKSPACE" => Some("backspace"),
        "KEY_SPACE" => Some(" "),
        _ => typed(name),
    }
}

// The words are static strings because a browser word outlives the key
// name it was read from, and a slice of that name would not. Two tables
// give every letter and digit a static word without an arm each.
const LETTERS: [&str; 26] = [
    "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s",
    "t", "u", "v", "w", "x", "y", "z",
];

const DIGITS: [&str; 10] = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"];

// One letter or digit key as the character it carries. The kernel names
// these keys after the character, KEY_A and KEY_7, so the one character
// after the prefix is the word. A longer tail, like KEY_KP0, is not one.
fn typed(name: &str) -> Option<&'static str> {
    let mut tail = name.strip_prefix("KEY_")?.chars();
    let letter = tail.next()?;
    if tail.next().is_some() {
        return None;
    }
    match letter {
        'A'..='Z' => Some(LETTERS[letter as usize - 'A' as usize]),
        '0'..='9' => Some(DIGITS[letter as usize - '0' as usize]),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Every name that carries a word of its own, so a binding that
    // changes shows up in one place.
    const BOUND: [(&str, &str); 15] = [
        ("KEY_UP", "up"),
        ("KEY_DOWN", "down"),
        ("KEY_LEFT", "left"),
        ("KEY_RIGHT", "right"),
        ("KEY_ENTER", "enter"),
        ("KEY_OK", "enter"),
        ("KEY_SELECT", "enter"),
        ("KEY_KPENTER", "enter"),
        ("KEY_BACK", "escape"),
        ("KEY_ESC", "escape"),
        ("KEY_EXIT", "escape"),
        ("KEY_HOMEPAGE", "home"),
        ("KEY_SEARCH", "search"),
        ("KEY_BACKSPACE", "backspace"),
        ("KEY_SPACE", " "),
    ];

    #[test]
    fn every_bound_name_carries_its_word() {
        for (name, word) in BOUND {
            assert_eq!(key_of(name), Some(word), "{name}");
        }
    }

    #[test]
    fn every_letter_and_every_digit_carry_the_character_itself() {
        assert_eq!(LETTERS.len(), 26);
        assert_eq!(DIGITS.len(), 10);
        for (index, word) in LETTERS.iter().enumerate() {
            let letter = (b'a' + index as u8) as char;
            assert_eq!(*word, letter.to_string(), "{letter}");
            let name = format!("KEY_{}", letter.to_ascii_uppercase());
            assert_eq!(key_of(&name), Some(*word), "{name}");
        }
        for (index, word) in DIGITS.iter().enumerate() {
            let digit = (b'0' + index as u8) as char;
            assert_eq!(*word, digit.to_string(), "{digit}");
            assert_eq!(key_of(&format!("KEY_{digit}")), Some(*word), "{digit}");
        }
    }

    // Names the browser binds nothing for, including ones that look like
    // a letter key and are not.
    const UNBOUND: [&str; 6] = ["KEY_F1", "KEY_KP0", "KEY_", "KEY_UNKNOWN", "A", "up"];

    #[test]
    fn a_name_the_browser_binds_nothing_for_carries_no_word() {
        for name in UNBOUND {
            assert_eq!(key_of(name), None, "{name}");
        }
    }
}
