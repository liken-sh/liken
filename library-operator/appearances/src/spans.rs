// The review's spans, drawn from the samples with no rule of the display's:
// each sample stands from its own time to the next sample's, and a span is
// a run of samples that name the same people, so a run with nobody named
// is a span with no people. The review shows these, and not the display's
// spans (player.rs), so each gap in the naming shows as a chapter of its
// own.

use serde::Serialize;

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Span {
    pub start: f64,
    pub end: f64,
    pub people: Vec<String>,
}

// `shots` is each sample's time with the people named in it, in time
// order, with each list sorted. The last sample ends at the video's end.
pub fn spans(shots: &[(f64, Vec<String>)], duration: f64) -> Vec<Span> {
    let mut spans: Vec<Span> = Vec::new();
    for (index, (start, people)) in shots.iter().enumerate() {
        let end = shots.get(index + 1).map_or(duration, |(next, _)| *next);
        match spans.last_mut() {
            Some(last) if &last.people == people => last.end = end,
            _ => spans.push(Span {
                start: *start,
                end,
                people: people.clone(),
            }),
        }
    }
    spans
}

// The spans as an ffmetadata chapters file, which mpv loads with
// --chapters-file. mpv draws a tick on the seek bar at each chapter, and
// the chapter's title is the span's people.
pub fn chapters(spans: &[Span]) -> String {
    let mut text = String::from(";FFMETADATA1\n");
    for span in spans {
        let title = if span.people.is_empty() {
            "(nobody named)".to_string()
        } else {
            span.people.join(", ")
        };
        text.push_str(&format!(
            "[CHAPTER]\nTIMEBASE=1/1000\nSTART={}\nEND={}\ntitle={}\n",
            (span.start * 1000.0).round() as i64,
            (span.end * 1000.0).round() as i64,
            escape(&title)
        ));
    }
    text
}

// ffmetadata gives '=', ';', '#', '\', and a newline a meaning, so each
// takes a backslash in a value.
fn escape(value: &str) -> String {
    let mut escaped = String::with_capacity(value.len());
    for c in value.chars() {
        if matches!(c, '=' | ';' | '#' | '\\' | '\n') {
            escaped.push('\\');
        }
        escaped.push(c);
    }
    escaped
}

#[cfg(test)]
mod tests {
    use super::*;

    fn shot(time: f64, people: &[&str]) -> (f64, Vec<String>) {
        (time, people.iter().map(|p| p.to_string()).collect())
    }

    #[test]
    fn spans_merge_shots_that_name_the_same_people() {
        let shots = [
            shot(0.0, &[]),
            shot(4.0, &["A", "B"]),
            shot(9.0, &["A", "B"]),
            shot(12.0, &["A"]),
        ];
        assert_eq!(
            spans(&shots, 20.0),
            vec![
                Span {
                    start: 0.0,
                    end: 4.0,
                    people: vec![]
                },
                Span {
                    start: 4.0,
                    end: 12.0,
                    people: vec!["A".into(), "B".into()]
                },
                Span {
                    start: 12.0,
                    end: 20.0,
                    people: vec!["A".into()]
                },
            ]
        );
    }

    #[test]
    fn chapters_write_milliseconds_and_escaped_titles() {
        let spans = [Span {
            start: 4.5,
            end: 12.25,
            people: vec!["Theodore \"Teddy\" = Sanders".into()],
        }];
        assert_eq!(
            chapters(&spans),
            ";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=4500\nEND=12250\ntitle=Theodore \"Teddy\" \\= Sanders\n"
        );
    }

    #[test]
    fn chapters_title_a_span_with_nobody() {
        let spans = [Span {
            start: 0.0,
            end: 1.0,
            people: vec![],
        }];
        assert!(chapters(&spans).contains("title=(nobody named)\n"));
    }
}
