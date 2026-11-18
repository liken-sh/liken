//! Stored descriptions as plain text.  Many projects copy their README's
//! first paragraph into `org.opencontainers.image.description`, and a
//! CRD's schema description is a Go doc comment, Markdown and all.  A
//! card shows the text as written, so the brackets and URL of a link,
//! the hashes of a heading, and emphasis marks appear as noise.

/// `text` with its Markdown marks taken out: a link or an image becomes
/// its text, and the marks of headings, quotes, list bullets, emphasis,
/// and code are removed.  An underscore stays, because names such as
/// `snake_case` contain one far more often than emphasis does.
pub fn plain(text: &str) -> String {
    let lines = text.lines().map(|line| {
        let line = line.trim_start();
        let line = line.trim_start_matches('#').trim_start_matches('>');
        let line = match line.trim_start().strip_prefix("- ") {
            Some(rest) => rest,
            None => line,
        };
        links(line.trim_start())
            .replace("**", "")
            .replace(['*', '`'], "")
    });
    lines.collect::<Vec<_>>().join("\n")
}

/// `line` with each `[text](url)` and `![text](url)` as its text.
fn links(line: &str) -> String {
    let mut out = String::with_capacity(line.len());
    let mut rest = line;
    while let Some(open) = rest.find('[') {
        let Some(close) = rest[open..].find("](").map(|at| open + at) else {
            break;
        };
        let Some(end) = rest[close..].find(')').map(|at| close + at) else {
            break;
        };
        let before = rest[..open].strip_suffix('!').unwrap_or(&rest[..open]);
        out.push_str(before);
        out.push_str(&rest[open + 1..close]);
        rest = &rest[end + 1..];
    }
    out.push_str(rest);
    out
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::plain_already("A media server", "A media server")]
    #[case::a_link(
        "A fork of [a series manager](https://manager.example.com) for movies",
        "A fork of a series manager for movies"
    )]
    #[case::an_image("![logo](https://example.com/logo.png) Fast", "logo Fast")]
    #[case::a_heading("# Cache\nKeeps sessions.", "Cache\nKeeps sessions.")]
    #[case::emphasis("A **fast** and *small* `cache`", "A fast and small cache")]
    #[case::a_list("- one\n- two", "one\ntwo")]
    #[case::an_underscore("Reads snake_case names", "Reads snake_case names")]
    #[case::a_bracket_alone("Holds [items] only", "Holds [items] only")]
    fn takes_out_markdown_marks(#[case] text: &str, #[case] expected: &str) {
        assert_eq!(plain(text), expected);
    }
}
