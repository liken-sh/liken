//! Cleaning of text that a card shows from the cluster or an image, such
//! as an image label, an annotation, or an `Intent` resource. Anyone who
//! can write the object controls the text, so each value is shortened
//! to fit a card, control characters and text direction marks are
//! removed, and a URL becomes a link only when it is plain `http` or
//! `https`. The templates also escape every value.

pub mod markdown;

/// The longest URL, in characters, that becomes a link.
const LINK_CHARS: usize = 300;

/// Whether a page may show `c` from untrusted text. Direction marks are
/// dropped because a right-to-left override reverses the text after it,
/// which can make a name or a log line look like something else.
pub fn shown(c: char) -> bool {
    let bidi = matches!(
        c,
        '\u{200E}' | '\u{200F}' | '\u{061C}' | '\u{202A}'..='\u{202E}' | '\u{2066}'..='\u{2069}'
    );
    !c.is_control() && !bidi
}

/// `text` as one line with whitespace collapsed and control characters
/// and direction marks removed, cut to `chars` characters with a final
/// ellipsis. It returns none when nothing is left.
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

/// `text`, trimmed, when it is an `http` or `https` URL of at most
/// `LINK_CHARS` printable ASCII characters with none of `"`, `'`, `<`,
/// `>`, `\`, or a backtick, which could end the attribute or tag around
/// it.
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
    fn collapses_text_to_one_plain_line(#[case] text: &str, #[case] expected: Option<&str>) {
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
    fn accepts_a_link_only_when_it_is_plain_http(
        #[case] text: &str,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(link(text).as_deref(), expected);
    }
}
