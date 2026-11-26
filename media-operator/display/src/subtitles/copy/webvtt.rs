//! WebVTT: a file of cues, and the markup of a cue's text, which a
//! Matroska `S_TEXT/WEBVTT` track also uses. The markup is converted as
//! FFmpeg's WebVTT decoder converts it: italic, bold, and underline become
//! ASS overrides, every other tag is dropped, and the entities are
//! decoded. The decoder ignores a cue's settings, so the copy does too.

use super::Dialogue;

/// The ASS text of a WebVTT cue.
pub fn markup(text: &str) -> String {
    let text = text.replace("\r\n", "\n");
    let text = text.trim_matches('\n');
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(open) = rest.find('<') {
        out.push_str(&entities(&rest[..open]));
        let after = &rest[open..];
        let Some(close) = after.find('>') else {
            rest = after;
            break;
        };
        let tag = after[1..close].trim();
        let (closing, name) = match tag.strip_prefix('/') {
            Some(name) => (true, name),
            None => (false, tag),
        };
        let name = name.split(['.', ' ']).next().unwrap_or_default();
        if let "i" | "b" | "u" = name {
            out.push_str(&format!("{{\\{name}{}}}", if closing { 0 } else { 1 }));
        }
        rest = &after[close + 1..];
    }
    out.push_str(&entities(rest));
    out.replace('\n', "\\N")
}

fn entities(text: &str) -> String {
    text.replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&nbsp;", "\u{a0}")
        .replace("&lrm;", "\u{200e}")
        .replace("&rlm;", "\u{200f}")
        .replace("&amp;", "&")
}

/// A WebVTT time, `HH:MM:SS.mmm` or `MM:SS.mmm`, in milliseconds.
pub fn time(text: &str) -> Option<u64> {
    let (clock, millis) = text.trim().split_once('.')?;
    let fields: Vec<u64> = clock
        .split(':')
        .map(|field| field.parse().ok())
        .collect::<Option<_>>()?;
    let seconds = match fields.as_slice() {
        [minutes, seconds] => minutes * 60 + seconds,
        [hours, minutes, seconds] => (hours * 60 + minutes) * 60 + seconds,
        _ => return None,
    };
    Some(seconds * 1000 + millis.get(..3)?.parse::<u64>().ok()?)
}

/// Every cue of a WebVTT file. A `NOTE`, `STYLE`, or `REGION` block has no
/// time line, so it is skipped.
pub fn parse(text: &str) -> Vec<Dialogue> {
    let text = text.replace("\r\n", "\n");
    let mut events = Vec::new();
    for block in text.split("\n\n") {
        let mut lines = block.lines().skip_while(|line| !line.contains("-->"));
        let Some(timing) = lines.next() else {
            continue;
        };
        let Some((start, end)) = timing.split_once("-->") else {
            continue;
        };
        let end = end.split_whitespace().next().unwrap_or_default();
        let (Some(start), Some(end)) = (time(start), time(end)) else {
            continue;
        };
        let body: Vec<&str> = lines.collect();
        events.push(Dialogue::text(start, end, markup(&body.join("\n"))));
    }
    events
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_markup_becomes_ass_overrides_and_text() {
        let cases = [
            ("<i>Leaning</i>\nnext", "{\\i1}Leaning{\\i0}\\Nnext"),
            ("<v Someone><c.loud>Hello</c></v>", "Hello"),
            ("Fish &amp; chips &lt;3", "Fish & chips <3"),
            ("At <00:01.500>times", "At times"),
        ];
        for (text, want) in cases {
            assert_eq!(markup(text), want, "{text:?}");
        }
    }

    #[test]
    fn a_file_gives_each_cue_with_its_times() {
        let file = "WEBVTT\n\nNOTE a note\n\nfirst\n00:00.500 --> 00:02.000 line:0\nOne line\n\n\
                    00:00:02.500 --> 00:00:04.000\nThe first\nand the second\n";
        assert_eq!(
            parse(file),
            [
                Dialogue::text(500, 2000, "One line".to_string()),
                Dialogue::text(2500, 4000, "The first\\Nand the second".to_string()),
            ]
        );
    }
}
