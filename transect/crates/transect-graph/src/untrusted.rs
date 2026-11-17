//! Text that someone stored in the cluster or an image, which a card
//! shows: an image's labels, an annotation, an `Intent` resource.  Anyone
//! who can write the object writes the text, so each value is cut to a
//! length a card can hold and stripped of control characters and of
//! the marks that change the direction text runs in, and a URL is kept
//! as a link only when it is plain `http` or `https`.  The templates
//! escape every value as well.

pub mod markdown;

/// The longest a link may be, in characters.
const LINK_CHARS: usize = 300;

/// Whether a page may show `c` from untrusted text: not a control
/// character, and not a mark that changes the direction text runs in.
/// A right-to-left override makes the text after it read backwards,
/// so a name or a log line could show as something else.
pub fn shown(c: char) -> bool {
    let bidi = matches!(
        c,
        '\u{200E}' | '\u{200F}' | '\u{061C}' | '\u{202A}'..='\u{202E}' | '\u{2066}'..='\u{2069}'
    );
    !c.is_control() && !bidi
}

/// `text` on one line, without control characters or direction marks,
/// cut to `chars` with an ellipsis, or none when nothing is left.
pub fn capped(text: &str, chars: usize) -> Option<String> {
    let line: String = text
        .chars()
        .map(|c| if c.is_whitespace() { ' ' } else { c })
        .filter(|c| shown(*c))
        .collect();
    let words: Vec<&str> = line.split(' ').filter(|word| !word.is_empty()).collect();
    let line = words.join(" ");
    if line.is_empty() {
        return None;
    }
    match line.chars().count() > chars {
        true => Some(line.chars().take(chars - 1).chain(['…']).collect()),
        false => Some(line),
    }
}

/// `text` as a link a page may hold: `http` or `https`, with nothing a
/// browser would read as the end of the URL.
pub fn link(text: &str) -> Option<String> {
    let text = text.trim();
    let scheme = text.starts_with("https://") || text.starts_with("http://");
    let plain = text
        .chars()
        .all(|c| c.is_ascii_graphic() && !"\"'<>\\`".contains(c));
    (scheme && plain && text.len() <= LINK_CHARS).then(|| text.to_string())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::plain("A database", Some("A database"))]
    #[case::lines_and_tabs("A\n\tdatabase\r\n", Some("A database"))]
    #[case::control_characters("A\u{7}\u{1b}[31m database", Some("A[31m database"))]
    #[case::blank("  \n ", None)]
    #[case::a_right_to_left_override("invoice\u{202E}fdp.exe", Some("invoicefdp.exe"))]
    #[case::an_isolate("a\u{2066}b\u{2069}c", Some("abc"))]
    #[case::direction_marks("a\u{200E}b\u{200F}c\u{061C}d", Some("abcd"))]
    #[case::right_to_left_text("שלום", Some("שלום"))]
    fn keeps_text_to_one_plain_line(#[case] text: &str, #[case] expected: Option<&str>) {
        assert_eq!(capped(text, 200).as_deref(), expected);
    }

    #[rstest]
    fn cuts_long_text_with_an_ellipsis() {
        let cut = capped(&"x".repeat(500), 200).unwrap();
        assert_eq!((cut.chars().count(), cut.ends_with('…')), (200, true));
    }

    #[rstest]
    #[case::https("https://docs.example.com/app", Some("https://docs.example.com/app"))]
    #[case::http("http://docs.example.com", Some("http://docs.example.com"))]
    #[case::javascript("javascript:alert(1)", None)]
    #[case::a_quote("https://docs.example.com/\"onmouseover=x", None)]
    #[case::a_space("https://docs.example.com/a b", None)]
    #[case::text("see the README", None)]
    fn keeps_a_link_only_when_it_is_plain_http(#[case] text: &str, #[case] expected: Option<&str>) {
        assert_eq!(link(text).as_deref(), expected);
    }
}
