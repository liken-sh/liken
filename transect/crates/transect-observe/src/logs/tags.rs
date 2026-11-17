//! This module tags the lines of a pod's tail with the patterns they
//! match.  For each pattern, transect sends the store the tail query
//! with the pattern added.  When a line of the tail is in that
//! response, with the same container, time, and text, the line gets the
//! pattern's tag.

use k8s_openapi::jiff::Timestamp;

use super::level::level;
use super::patterns::LogPattern;
use super::query::{Row, Values, fill};
use super::source::Reached;
use super::tail::{self, Line, Matching, Read, Tag, Tail};

/// The tail query of `pod`, narrowed to the lines that `pattern`
/// matches, across the pod's containers.  It requests only the fields
/// that `tag` matches a line by.
pub fn query(
    reached: &Reached,
    (namespace, name, since): (&str, &str, Option<Timestamp>),
    pattern: &LogPattern,
) -> String {
    let source = &reached.source;
    let language = source.language;
    let values = Values {
        namespace: Some(namespace),
        pod: Some(name),
        since,
        limit: Some(tail::TAIL_LINES as usize),
        ..Values::default()
    };
    let query = fill(&source.tail, language, &source.fields, &values);
    let query = language.narrow(&query, &format!("({})", pattern.matches));
    let fields = &source.fields;
    language.keep(&query, &[&fields.time, &fields.message, &fields.container])
}

/// The lines in `rows`, the response to `pattern`'s query, newest last,
/// or `None` when `rows` contains no line.
pub fn matching(pattern: &LogPattern, rows: &[Row], reached: &Reached) -> Option<Matching> {
    let fields = &reached.source.fields;
    let mut lines: Vec<(String, Line)> = rows
        .iter()
        .filter_map(|row| {
            let text = row.get(&fields.message)?;
            if tail::blank(text) {
                return None;
            }
            let line = Line {
                level: level(text),
                text: tail::cut(text),
                time: row.get(&fields.time).and_then(|time| time.parse().ok()),
                tags: Vec::new(),
            };
            Some((
                row.get(&fields.container).cloned().unwrap_or_default(),
                line,
            ))
        })
        .collect();
    lines.reverse();
    (!lines.is_empty()).then(|| Matching {
        tag: tag_of(pattern),
        lines,
    })
}

fn tag_of(pattern: &LogPattern) -> Tag {
    Tag {
        pattern: pattern.name.clone(),
        meaning: pattern.meaning.clone(),
        severity: pattern.severity,
    }
}

/// Adds `pattern`'s tag to each line of `tails` that is in `rows`, the
/// response to the pattern's query.
pub fn tag(tails: &mut [Tail], pattern: &LogPattern, rows: &[Row], reached: &Reached) {
    let fields = &reached.source.fields;
    let matched: Vec<(&str, Timestamp, String)> = rows
        .iter()
        .filter_map(|row| {
            let container = row.get(&fields.container)?.as_str();
            let time = row.get(&fields.time)?.parse().ok()?;
            let text = tail::cut(row.get(&fields.message)?);
            Some((container, time, text))
        })
        .collect();
    for one in tails.iter_mut() {
        let Read::Lines(lines) = &mut one.read else {
            continue;
        };
        for line in lines.iter_mut() {
            let Some(time) = line.time else { continue };
            let hit = matched.iter().any(|(container, at, text)| {
                *container == one.container && *at == time && *text == line.text
            });
            if hit {
                line.tags.push(tag_of(pattern));
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::patterns::Severity;
    use super::super::patterns::fixtures::pattern;
    use super::super::source::fixtures::source;
    use super::*;

    fn row(container: &str, time: &str, message: &str) -> Row {
        [
            ("k8s.container.name", container),
            ("_time", time),
            ("_msg", message),
            ("severity_text", "INFO"),
        ]
        .into_iter()
        .map(|(key, value)| (key.to_string(), value.to_string()))
        .collect()
    }

    fn tails(reached: &Reached) -> Vec<Tail> {
        let rows = [
            row("web", "2026-01-01T12:00:02Z", "dial db: Connection refused"),
            row("web", "2026-01-01T12:00:01Z", "ready"),
        ];
        let proxy = [row(
            "proxy",
            "2026-01-01T12:00:02Z",
            "dial db: Connection refused",
        )];
        vec![
            Tail {
                container: "web".into(),
                read: tail::from_rows(&rows, &reached.source),
            },
            Tail {
                container: "proxy".into(),
                read: tail::from_rows(&proxy, &reached.source),
            },
        ]
    }

    fn tagged(tails: &[Tail]) -> Vec<(String, String, Vec<String>)> {
        tails
            .iter()
            .flat_map(|one| match &one.read {
                Read::Lines(lines) => lines
                    .iter()
                    .map(|line| {
                        let tags = line.tags.iter().map(|tag| tag.pattern.clone()).collect();
                        (one.container.clone(), line.text.clone(), tags)
                    })
                    .collect(),
                _ => Vec::new(),
            })
            .collect()
    }

    #[rstest]
    fn tags_the_lines_the_pattern_answer_holds_in_their_own_container() {
        let reached = source().reached(None, "cluster.local");
        let mut tails = tails(&reached);
        let refused = pattern("refused", r#"_msg:~"refused""#, Severity::Error);
        let rows = [row(
            "web",
            "2026-01-01T12:00:02Z",
            "dial db: Connection refused",
        )];
        tag(&mut tails, &refused, &rows, &reached);
        let refused = vec!["refused".to_string()];
        assert_eq!(
            tagged(&tails),
            vec![
                ("web".into(), "ready".into(), vec![]),
                ("web".into(), "dial db: Connection refused".into(), refused),
                ("proxy".into(), "dial db: Connection refused".into(), vec![]),
            ]
        );
    }

    #[rstest]
    #[case::two_lines(
        vec![
            row("web", "2026-01-01T12:00:02Z", "dial db: Connection refused"),
            row("proxy", "2026-01-01T12:00:01Z", "upstream: connection refused"),
        ],
        vec![("proxy", "upstream: connection refused"), ("web", "dial db: Connection refused")]
    )]
    #[case::none(vec![], vec![])]
    fn keeps_the_lines_a_pattern_matched_newest_last(
        #[case] rows: Vec<Row>,
        #[case] expected: Vec<(&str, &str)>,
    ) {
        let reached = source().reached(None, "cluster.local");
        let refused = pattern("refused", r#"_msg:~"refused""#, Severity::Error);
        let found = matching(&refused, &rows, &reached);
        let lines: Vec<(String, String)> = found
            .into_iter()
            .flat_map(|found| found.lines)
            .map(|(container, line)| (container, line.text))
            .collect();
        let expected: Vec<(String, String)> = expected
            .into_iter()
            .map(|(container, text)| (container.into(), text.into()))
            .collect();
        assert_eq!(lines, expected);
    }

    #[rstest]
    fn asks_for_the_tail_with_the_pattern_added() {
        let reached = source().reached(None, "cluster.local");
        let refused = pattern("refused", r#"_msg:~"refused""#, Severity::Error);
        let since = "2026-01-01T00:00:00Z".parse().ok();
        assert_eq!(
            query(&reached, ("shop", "web-1", since), &refused),
            r#"(_time:>=2026-01-01T00:00:00Z k8s.namespace.name:"shop" k8s.pod.name:"web-1") (_msg:~"refused") | sort by (_time) desc | limit 50 | fields _time, _msg, k8s.container.name"#
        );
    }
}
