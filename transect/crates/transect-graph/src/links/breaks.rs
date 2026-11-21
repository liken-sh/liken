//! Where a value that is not an object's name may break across lines.
//! A value such as an address or a list of channels is one long token
//! to the browser, so a narrow box breaks it at its last letter that
//! fits, as in `assistan` and `t.svc`. The `spelled` macro in
//! `link.html` writes a `<wbr>` between the pieces that `pieces`
//! returns, so the browser breaks after a separator first. The template
//! escapes each piece as it escapes any other text. A copy of the text
//! does not include a `<wbr>`, and it would include a zero-width space.

/// The characters that a line may break after.
const SEPARATORS: &[char] = &[',', '.', '/', ':', '-'];

/// `text` cut after each separator, so the pieces joined are `text`.
pub fn pieces(text: &str) -> Vec<&str> {
    text.split_inclusive(SEPARATORS).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_list_of_channels("FL,FR,LFE", vec!["FL,", "FR,", "LFE"])]
    #[case::an_address("bus.shop.svc:1883", vec!["bus.", "shop.", "svc:", "1883"])]
    #[case::a_path("example.test/web-ui", vec!["example.", "test/", "web-", "ui"])]
    #[case::no_separator("plain", vec!["plain"])]
    #[case::a_separator_at_the_end("left-", vec!["left-"])]
    #[case::separators_together("a--b", vec!["a-", "-", "b"])]
    #[case::markup_left_whole("a<b&c-d", vec!["a<b&c-", "d"])]
    #[case::nothing("", vec![])]
    fn cuts_a_value_after_each_separator(#[case] text: &str, #[case] expected: Vec<&str>) {
        assert_eq!(pieces(text), expected);
    }
}
