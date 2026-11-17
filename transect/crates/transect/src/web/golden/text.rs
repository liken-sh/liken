//! A page's visible text: its HTML with the tags stripped, one block to
//! a line, so a golden changes when a page's words do and not when its
//! markup does.  A log line's level is drawn as a color, so the text
//! names it in brackets before the line, as `[error]`.  The goldens are drawn at the fixture's pinned time, so
//! every age reads the same on each run.

/// The tags that run inside a line of text.  Every other tag ends a
/// line, so each block of the page reads on a line of its own.
const INLINE: &[&str] = &[
    "a", "abbr", "b", "code", "em", "i", "kbd", "label", "mark", "s", "small", "span", "strong",
    "sub", "sup", "time", "u",
];

/// The tags that mark a place a line may break, which a reader never
/// sees, so the words around one run together.
const BREAKS: &[&str] = &["wbr"];

/// The opening tag of one part of a name such as `namespace/name`,
/// which the link macro wraps as a whole.  A reader reads the parts as
/// one word, so the part's tags part nothing.
const NAME_PART: &str = "<span class=\"name-part\">";

/// The elements whose contents a reader never sees as text.
const HIDDEN: &[&str] = &["script", "style", "template"];

/// The visible text of `html`, a line per block.
pub fn visible(html: &str) -> String {
    let mut lines: Vec<String> = Vec::new();
    let mut line = String::new();
    let mut rest = html;
    let mut in_part = false;
    while let Some(open) = rest.find('<') {
        line.push_str(&rest[..open]);
        rest = &rest[open..];
        if let Some(after) = rest.strip_prefix("<!--") {
            rest = after.find("-->").map_or("", |end| &after[end + 3..]);
            continue;
        }
        let end = rest.find('>').map_or(rest.len(), |end| end + 1);
        let tag = &rest[..end];
        let name = tag_name(tag);
        rest = &rest[end..];
        if HIDDEN.contains(&name.as_str()) && !rest.is_empty() {
            let close = format!("</{name}");
            rest = rest.find(&close).map_or("", |at| &rest[at..]);
            let end = rest.find('>').map_or(rest.len(), |end| end + 1);
            rest = &rest[end..];
        }
        let ends_part = in_part && tag == "</span>";
        in_part = tag == NAME_PART || (in_part && !ends_part);
        let joins = BREAKS.contains(&name.as_str()) || tag == NAME_PART || ends_part;
        match (joins, INLINE.contains(&name.as_str())) {
            (true, _) => {}
            (false, true) => line.push(' '),
            (false, false) => lines.push(std::mem::take(&mut line)),
        }
        line.push_str(level(tag));
    }
    line.push_str(rest);
    lines.push(line);
    let read = lines.iter().map(|line| collapsed(&decoded(line)));
    let read: Vec<String> = read.filter(|line| !line.is_empty()).collect();
    read.join("\n") + "\n"
}

/// The level a log line's tag draws it at, as the text names it.
fn level(tag: &str) -> &'static str {
    let classes = tag
        .split_once("class=\"")
        .map_or("", |(_, after)| after.split('"').next().unwrap_or_default());
    let classes: Vec<&str> = classes.split_whitespace().collect();
    match () {
        _ if classes.contains(&"log-error") => "[error] ",
        _ if classes.contains(&"log-warning") => "[warning] ",
        _ => "",
    }
}

/// The lower-case name of the tag `tag`, such as `div` for `</div>`.
fn tag_name(tag: &str) -> String {
    let inner = tag.trim_start_matches('<').trim_start_matches('/');
    let name = inner.split(|c: char| c.is_whitespace() || c == '>' || c == '/');
    name.into_iter()
        .next()
        .unwrap_or_default()
        .to_ascii_lowercase()
}

/// `text` with its character references read.
fn decoded(text: &str) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(at) = rest.find('&') {
        out.push_str(&rest[..at]);
        rest = &rest[at..];
        let end = rest.find(';').filter(|end| *end <= 10);
        let read = end.and_then(|end| reference(&rest[1..end]).map(|c| (c, end)));
        match read {
            Some((c, end)) => {
                out.push(c);
                rest = &rest[end + 1..];
            }
            None => {
                out.push('&');
                rest = &rest[1..];
            }
        }
    }
    out.push_str(rest);
    out
}

fn reference(name: &str) -> Option<char> {
    let named = match name {
        "amp" => Some('&'),
        "lt" => Some('<'),
        "gt" => Some('>'),
        "quot" => Some('"'),
        "apos" => Some('\''),
        "nbsp" => Some(' '),
        "middot" => Some('·'),
        "rarr" => Some('→'),
        "larr" => Some('←'),
        "hellip" => Some('…'),
        "mdash" => Some('—'),
        "ndash" => Some('–'),
        _ => None,
    };
    let number = || {
        let digits = name.strip_prefix('#')?;
        let code = match digits.strip_prefix(['x', 'X']) {
            Some(hex) => u32::from_str_radix(hex, 16).ok()?,
            None => digits.parse().ok()?,
        };
        char::from_u32(code)
    };
    named.or_else(number)
}

/// `text` with each run of white space one space, and none at its ends.
fn collapsed(text: &str) -> String {
    text.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::blocks_on_lines("<div>one</div><div>two</div>", "one\ntwo\n")]
    #[case::inline_in_a_line("<p>a <a href=\"/x\">link</a> here</p>", "a link here\n")]
    #[case::scripts_and_styles_hidden(
        "<style>p{}</style><p>shown</p><script>let a = 1 < 2;</script>",
        "shown\n"
    )]
    #[case::comments_hidden("<p>a<!-- not <b>this</b> --> b</p>", "a b\n")]
    #[case::references_read("<p>a &amp; b &#x27;c&#39; &middot; d</p>", "a & b 'c' · d\n")]
    #[case::white_space_collapsed("<p>\n  a\n   b  </p>", "a b\n")]
    #[case::a_line_break_parts_nothing("<p>shop/<wbr>web</p>", "shop/web\n")]
    #[case::a_names_parts_part_nothing(
        "<p>Deployment <span class=\"name-part\">shop/</span><wbr><span class=\"name-part\">web</span> is down</p>",
        "Deployment shop/web is down\n"
    )]
    #[case::inline_tags_part_words("<p><b>Down</b><i>critical</i></p>", "Down critical\n")]
    #[case::log_levels_named(
        "<div class=\"log-line log-error\">refused</div><div class=\"log-line\">ok</div>",
        "[error] refused\nok\n"
    )]
    #[case::figures_kept(
        "<p>started 40d ago · 3.0Gi · 38% · 250m</p>",
        "started 40d ago · 3.0Gi · 38% · 250m\n"
    )]
    fn reads_the_visible_text(#[case] html: &str, #[case] text: &str) {
        assert_eq!(visible(html), text);
    }
}
