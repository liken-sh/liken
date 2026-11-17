//! An alert's summary and description as HTML.  The text comes from an
//! alert rule's annotations, which authors write for Alertmanager and
//! Grafana, so it can contain inline Markdown: links, bare URLs, bold,
//! emphasis, and code.  transect does not control who writes the rules,
//! so this module escapes every character of the text, makes a link
//! only for an `http://` or `https://` address, and does not parse
//! block-level Markdown, such as headings and lists, or images.  An
//! image's `!` stays as text before the link its brackets make.

/// `text` escaped for HTML, with its inline Markdown drawn.
pub fn inline(text: &str) -> String {
    let mut html = String::with_capacity(text.len());
    let mut rest = text;
    let mut previous = None;
    while let Some(first) = rest.chars().next() {
        let (drawn, used) = token(rest, previous).unwrap_or_else(|| {
            let mut one = String::new();
            escape_into(&mut one, first);
            (one, first.len_utf8())
        });
        html.push_str(&drawn);
        previous = rest[..used].chars().last();
        rest = &rest[used..];
    }
    html
}

/// `text` escaped for HTML, and nothing else.
pub fn escape(text: &str) -> String {
    let mut html = String::with_capacity(text.len());
    text.chars().for_each(|c| escape_into(&mut html, c));
    html
}

fn escape_into(html: &mut String, c: char) {
    match c {
        '&' => html.push_str("&amp;"),
        '<' => html.push_str("&lt;"),
        '>' => html.push_str("&gt;"),
        '"' => html.push_str("&quot;"),
        '\'' => html.push_str("&#39;"),
        c => html.push(c),
    }
}

/// The HTML for the mark at the start of `rest` and how many bytes of
/// `rest` it used, or `None` when `rest` starts with plain text.
fn token(rest: &str, previous: Option<char>) -> Option<(String, usize)> {
    match rest.chars().next()? {
        '[' => link(rest),
        '`' => code(rest),
        '*' if rest.starts_with("**") => Some(strong(rest)),
        '*' => emphasis(rest, '*', previous),
        '_' => emphasis(rest, '_', previous),
        'h' => bare_url(rest, previous),
        _ => None,
    }
}

fn is_web(url: &str) -> bool {
    let after = url
        .strip_prefix("https://")
        .or_else(|| url.strip_prefix("http://"));
    after.is_some_and(|after| {
        !after.is_empty() && !url.chars().any(|c| c.is_whitespace() || c.is_control())
    })
}

fn anchor(url: &str, label: &str) -> String {
    format!(
        "<a href=\"{}\" target=\"_blank\" rel=\"noopener noreferrer\">{label}</a>",
        escape(url)
    )
}

/// `[label](url)`, when the address is a web address.
fn link(rest: &str) -> Option<(String, usize)> {
    let close = rest.find(']')?;
    let after = rest[close + 1..].strip_prefix('(')?;
    let end = after.find(')')?;
    let url = after[..end].trim();
    let label = &rest[1..close];
    (is_web(url) && !label.is_empty()).then(|| (anchor(url, &escape(label)), close + 2 + end + 1))
}

/// A bare web address in the text, without the punctuation that ends
/// the sentence around it.
fn bare_url(rest: &str, previous: Option<char>) -> Option<(String, usize)> {
    if previous.is_some_and(char::is_alphanumeric) {
        return None;
    }
    let end = rest.find(char::is_whitespace).unwrap_or(rest.len());
    let url =
        rest[..end].trim_end_matches(['.', ',', ';', ':', '!', '?', ')', ']', '*', '_', '\'', '"']);
    is_web(url).then(|| (anchor(url, &escape(url)), url.len()))
}

fn code(rest: &str) -> Option<(String, usize)> {
    let end = rest[1..].find('`')?;
    let inner = &rest[1..1 + end];
    (!inner.is_empty()).then(|| (format!("<code>{}</code>", escape(inner)), end + 2))
}

/// `**bold**`.  An unclosed pair stays as two plain asterisks.
fn strong(rest: &str) -> (String, usize) {
    let Some(end) = rest[2..].find("**") else {
        return ("**".into(), 2);
    };
    let inner = &rest[2..2 + end];
    if inner.is_empty()
        || inner.starts_with(char::is_whitespace)
        || inner.ends_with(char::is_whitespace)
    {
        return ("**".into(), 2);
    }
    (format!("<strong>{}</strong>", inline(inner)), end + 4)
}

/// `*emphasis*` or `_emphasis_`.  An underscore inside a word, as in
/// `snake_case`, does not mark emphasis.
fn emphasis(rest: &str, mark: char, previous: Option<char>) -> Option<(String, usize)> {
    let end = rest[1..].find(mark)?;
    let inner = &rest[1..1 + end];
    let around_word = previous.is_some_and(char::is_alphanumeric)
        || rest[2 + end..]
            .chars()
            .next()
            .is_some_and(char::is_alphanumeric);
    let spaced = inner.starts_with(char::is_whitespace) || inner.ends_with(char::is_whitespace);
    let refused = inner.is_empty() || spaced || (mark == '_' && around_word);
    (!refused).then(|| (format!("<em>{}</em>", inline(inner)), end + 2))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const LINK: &str = "<a href=\"https://docs.example.test/a?x=1&amp;y=2\" target=\"_blank\" rel=\"noopener noreferrer\">";

    #[rstest]
    #[case::plain("Disk is 85% full", "Disk is 85% full")]
    #[case::a_link(
        "See [the forecast](https://docs.example.test/a?x=1&y=2) now",
        format!("See {LINK}the forecast</a> now")
    )]
    #[case::a_bare_url(
        "See https://docs.example.test/a?x=1&y=2.",
        format!("See {LINK}https://docs.example.test/a?x=1&amp;y=2</a>.")
    )]
    #[case::a_bare_url_in_parentheses(
        "(https://docs.example.test/a?x=1&y=2)",
        format!("({LINK}https://docs.example.test/a?x=1&amp;y=2</a>)")
    )]
    #[case::bold("a **big** day", "a <strong>big</strong> day")]
    #[case::star_emphasis("a *slow* day", "a <em>slow</em> day")]
    #[case::underscore_emphasis("a _slow_ day", "a <em>slow</em> day")]
    #[case::snake_case("the snake_case_name", "the snake_case_name")]
    #[case::code("run `kubectl get <pod>`", "run <code>kubectl get &lt;pod&gt;</code>")]
    #[case::markup_in_code("`**no**`", "<code>**no**</code>")]
    #[case::javascript_link("[click](javascript:alert(1))", "[click](javascript:alert(1))")]
    #[case::relative_link("[docs](/docs)", "[docs](/docs)")]
    #[case::script(
        "<script>alert('x')</script>",
        "&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt;"
    )]
    #[case::quote_in_label(
        "[a\"b](https://example.test/)",
        "<a href=\"https://example.test/\" target=\"_blank\" rel=\"noopener noreferrer\">a&quot;b</a>"
    )]
    #[case::unclosed_bracket("a [broken link", "a [broken link")]
    #[case::unclosed_paren(
        "[x](https://example.test/",
        "[x](<a href=\"https://example.test/\" target=\"_blank\" rel=\"noopener noreferrer\">https://example.test/</a>"
    )]
    #[case::unclosed_star("a *loose end", "a *loose end")]
    #[case::unclosed_bold("a **loose end", "a **loose end")]
    #[case::unclosed_code("a `loose end", "a `loose end")]
    #[case::spaced_stars("2 * 3 * 4", "2 * 3 * 4")]
    #[case::heading("# Title", "# Title")]
    #[case::image(
        "![logo](https://example.test/l.png)",
        "!<a href=\"https://example.test/l.png\" target=\"_blank\" rel=\"noopener noreferrer\">logo</a>"
    )]
    #[case::not_a_url(
        "the http protocol, and ahttps://x.test",
        "the http protocol, and ahttps://x.test"
    )]
    fn draws_inline_markdown(#[case] text: &str, #[case] expected: impl AsRef<str>) {
        assert_eq!(inline(text), expected.as_ref());
    }

    #[test]
    fn escapes_without_drawing() {
        assert_eq!(escape("<b>*a*</b>"), "&lt;b&gt;*a*&lt;/b&gt;");
    }
}
