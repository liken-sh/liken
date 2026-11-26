//! MicroDVD: a text `.sub` file whose cues are timed in frames of the
//! video, as `{start}{end}text`. A `.sub` file beside an `.idx` file is a
//! VobSub track of pictures, and mpv reports it as `dvd_subtitle`, so it
//! never reaches this module.

use super::Dialogue;

/// The frame rate that FFmpeg's MicroDVD reader takes when neither the file
/// nor the video gives one.
const DEFAULT_FPS: f64 = 23.976;

/// Every cue of a MicroDVD file. A first cue `{1}{1}23.976` states the
/// frame rate, and otherwise `fps`, the video's rate, times the cues. It is
/// `None` when the rate is not a positive number, or a frame's time does
/// not fit.
pub fn parse(text: &str, fps: Option<f64>) -> Option<Vec<Dialogue>> {
    let mut rate = fps.filter(|fps| *fps > 0.0).unwrap_or(DEFAULT_FPS);
    let mut events = Vec::new();
    for (index, line) in text.lines().enumerate() {
        let Some((start, end, body)) = cue(line.trim()) else {
            continue;
        };
        if index == 0
            && start == 1
            && end == Some(1)
            && let Ok(stated) = body.trim().parse::<f64>()
        {
            rate = stated;
            continue;
        }
        if !(rate.is_finite() && rate > 0.0) {
            return None;
        }
        let at = |frame: u64| {
            let ms = (frame as f64 * 1000.0 / rate).round();
            (ms.is_finite() && ms < u64::MAX as f64 / 2.0).then_some(ms as u64)
        };
        let start_ms = at(start)?;
        let end = match end {
            Some(end) => at(end)?,
            None => start_ms + 3000,
        };
        events.push(Dialogue::text(start_ms, end, markup(body)));
    }
    Some(events)
}

/// The frames and the text of one line, `{start}{end}text`. A cue whose end
/// is empty has no end.
fn cue(line: &str) -> Option<(u64, Option<u64>, &str)> {
    let rest = line.strip_prefix('{')?;
    let (start, rest) = rest.split_once('}')?;
    let rest = rest.strip_prefix('{')?;
    let (end, body) = rest.split_once('}')?;
    Some((start.parse().ok()?, end.parse().ok(), body))
}

/// The ASS text of a cue. `|` breaks a line. The style codes of italic,
/// bold, underline, and strikeout become ASS overrides for the whole cue,
/// and every other `{x:...}` code is dropped.
fn markup(body: &str) -> String {
    let mut out = String::new();
    let mut rest = body;
    while let Some(open) = rest.find('{') {
        out.push_str(&rest[..open]);
        let after = &rest[open + 1..];
        let Some(close) = after.find('}') else {
            rest = &rest[open..];
            break;
        };
        let code = &after[..close];
        if let Some((kind, value)) = code.split_once(':')
            && kind.eq_ignore_ascii_case("y")
        {
            for letter in ["i", "b", "u", "s"] {
                if value.to_ascii_lowercase().contains(letter) {
                    out.push_str(&format!("{{\\{letter}1}}"));
                }
            }
        }
        rest = &after[close + 1..];
    }
    out.push_str(rest);
    out.replace('|', "\\N")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_first_line_states_the_frame_rate() {
        let file = "{1}{1}25\n{25}{50}One line\n{100}{150}{y:i}The first|and the second\n";
        assert_eq!(
            parse(file, Some(23.976)).unwrap_or_default(),
            [
                Dialogue::text(1000, 2000, "One line".to_string()),
                Dialogue::text(4000, 6000, "{\\i1}The first\\Nand the second".to_string()),
            ]
        );
    }

    /// A rate of 0 would divide by zero, and a frame past the end of time
    /// would not fit, so each fails the read.
    #[test]
    fn a_rate_or_a_frame_out_of_range_fails() {
        assert_eq!(parse("{1}{1}0\n{10}{20}x\n", None), None);
        assert_eq!(parse("{18446744073709551615}{1}x\n", Some(0.001)), None);
    }

    #[test]
    fn the_video_rate_times_a_file_that_states_none() {
        let events = parse("{48}{96}{c:$0000ff}Plain\n", Some(24.0)).unwrap_or_default();
        assert_eq!(events, [Dialogue::text(2000, 4000, "Plain".to_string())]);
    }
}
