//! SubRip: a file of numbered cues, and the HTML-like markup of each cue's
//! text, which a Matroska `S_TEXT/UTF8` track also uses. The markup is
//! converted as FFmpeg's SubRip decoder converts it for mpv.

use super::Dialogue;

/// The ASS text of a SubRip cue. Each line break becomes `\N`, the tags of
/// italic, bold, underline, and strikeout become ASS overrides, and a
/// `font` tag's colour becomes `\c`. An ASS override that the cue already
/// has, such as `{\an8}`, is kept. Another tag is dropped.
pub fn markup(text: &str) -> String {
    let text = overrides(&text.replace("\r\n", "\n"));
    let text = text.trim_matches('\n');
    let mut out = String::with_capacity(text.len() + 16);
    let mut rest = text;
    while let Some(open) = rest.find('<') {
        out.push_str(&rest[..open].replace('\n', "\\N"));
        let after = &rest[open..];
        let Some(close) = after.find('>') else {
            rest = after;
            break;
        };
        let tag = &after[1..close];
        out.push_str(&tag_override(tag));
        rest = &after[close + 1..];
    }
    out.push_str(&rest.replace('\n', "\\N"));
    out
}

/// Keeps the first `{\anN}` of a cue and drops every other ASS override
/// block, as FFmpeg's SubRip decoder does. A brace that does not start an
/// override is text.
fn overrides(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut kept = false;
    let mut rest = text;
    while let Some(open) = rest.find("{\\") {
        out.push_str(&rest[..open]);
        let after = &rest[open..];
        let Some(close) = after.find('}') else {
            rest = after;
            break;
        };
        let block = &after[..=close];
        let alignment = block.len() == 6
            && block.starts_with("{\\an")
            && block.as_bytes()[4].is_ascii_digit()
            && block.as_bytes()[4] != b'0';
        if alignment && !kept {
            out.push_str(block);
            kept = true;
        }
        rest = &after[close + 1..];
    }
    out.push_str(rest);
    out
}

/// The ASS override for one tag, without its angle brackets.
fn tag_override(tag: &str) -> String {
    let lower = tag.trim().to_ascii_lowercase();
    let (closing, name) = match lower.strip_prefix('/') {
        Some(name) => (true, name.trim()),
        None => (false, lower.as_str()),
    };
    let flag = |letter: &str| format!("{{\\{letter}{}}}", if closing { 0 } else { 1 });
    match name.split_whitespace().next().unwrap_or_default() {
        "i" => flag("i"),
        "b" => flag("b"),
        "u" => flag("u"),
        "s" => flag("s"),
        "font" if closing => "{\\c}".to_string(),
        "font" => colour(&lower).map_or_else(String::new, |bgr| format!("{{\\c&H{bgr}&}}")),
        _ => String::new(),
    }
}

/// The colour of a `font` tag as ASS writes it, blue first, from a
/// `color="#rrggbb"` attribute.
fn colour(tag: &str) -> Option<String> {
    let at = tag.find("color")?;
    let value = tag[at + 5..].trim_start_matches([' ', '=', '"', '\'']);
    let hex = value.strip_prefix('#').unwrap_or(value);
    let hex = hex.get(..6)?;
    hex.chars()
        .all(|c| c.is_ascii_hexdigit())
        .then(|| format!("{}{}{}", &hex[4..6], &hex[2..4], &hex[0..2]).to_ascii_uppercase())
}

/// A SubRip time, `HH:MM:SS,mmm`, in milliseconds. A full stop or a colon
/// in place of the comma is read too, as FFmpeg reads it.
pub fn time(text: &str) -> Option<u64> {
    let fields: Vec<&str> = text.trim().split([':', ',', '.']).collect();
    let [hours, minutes, seconds, millis] = fields.as_slice() else {
        return None;
    };
    let number = |field: &str| field.trim().parse::<u64>().ok();
    let millis = millis.trim();
    let millis = number(millis.get(..3.min(millis.len()))?)?;
    let seconds = number(hours)?
        .checked_mul(60)?
        .checked_add(number(minutes)?)?
        .checked_mul(60)?
        .checked_add(number(seconds)?)?;
    seconds.checked_mul(1000)?.checked_add(millis)
}

/// Every cue of a SubRip file. A block whose time line does not parse is
/// skipped.
pub fn parse(text: &str) -> Vec<Dialogue> {
    let text = text.replace("\r\n", "\n");
    let mut events = Vec::new();
    let mut lines = text.lines().peekable();
    while let Some(line) = lines.next() {
        let Some((start, end)) = line.split_once("-->") else {
            continue;
        };
        let (Some(start), Some(end)) = (
            time(start),
            time(end.split_whitespace().next().unwrap_or_default()),
        ) else {
            continue;
        };
        let mut body = Vec::new();
        while let Some(next) = lines.peek() {
            if next.trim().is_empty() {
                break;
            }
            body.push(*next);
            lines.next();
        }
        events.push(Dialogue::text(start, end, markup(&body.join("\n"))));
    }
    events
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_markup_becomes_ass_overrides() {
        let cases = [
            ("One line", "One line"),
            ("Two\r\nlines\r\n", "Two\\Nlines"),
            (
                "<i>Leaning</i> and <b>bold</b>",
                "{\\i1}Leaning{\\i0} and {\\b1}bold{\\b0}",
            ),
            (
                "<font color=\"#ff8000\">Orange</font>",
                "{\\c&H0080FF&}Orange{\\c}",
            ),
            ("{\\an8}At the top", "{\\an8}At the top"),
            ("{\\an8}Top{\\an2} and {\\b1}bold", "{\\an8}Top and bold"),
            ("{\\pos(10,10)}Placed", "Placed"),
            ("A {brace}", "A {brace}"),
            ("<v Someone>Hello", "Hello"),
            ("A < B", "A < B"),
        ];
        for (text, want) in cases {
            assert_eq!(markup(text), want, "{text:?}");
        }
    }

    #[test]
    fn each_time_form_that_ffmpeg_reads_is_read() {
        let cases = [
            ("00:00:01,500", Some(1500)),
            ("00:00:01.500", Some(1500)),
            ("00:00:01:500", Some(1500)),
            ("01:02:03,004", Some(3_723_004)),
            ("00:01,500", None),
            ("99999999999999999:00:00,000", None),
        ];
        for (text, want) in cases {
            assert_eq!(time(text), want, "{text}");
        }
    }

    #[test]
    fn a_file_gives_each_cue_with_its_times() {
        let file = "\u{feff}1\r\n00:00:00,500 --> 00:00:02,000\r\nOne line\r\n\r\n\
                    2\r\n00:00:02.500 --> 00:00:04,000 X1:0\r\nThe first\r\nand the second\r\n\r\n\
                    3\r\nbroken --> time\r\nskipped\r\n";
        assert_eq!(
            parse(file),
            [
                Dialogue::text(500, 2000, "One line".to_string()),
                Dialogue::text(2500, 4000, "The first\\Nand the second".to_string()),
            ]
        );
    }
}
