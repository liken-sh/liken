// The one rule that says a play finished its work. `library-operator`'s
// `watched.go` states the same rule for the marks it writes to Jellyfin,
// and `watched.json` beside this file holds the cases both sides test
// against, so the browser and the Jellyfin marks agree on every play.

/// The two amounts of a work that may remain when it counts as watched: a
/// share of its length, and a number of seconds. The rule takes whichever
/// leaves less time, so the seconds apply only to a work over 100 minutes.
/// Credits run past the story, so a play that stopped in them counts as
/// watched.
pub const WATCHED_PERCENT: i64 = 5;
pub const WATCHED_SECONDS: i64 = 300;

/// One credits span of the work's main file, in seconds from the start of
/// the file. An absent start is the start of the file, and an absent end
/// is the end of the file.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Credits {
    pub start: Option<f64>,
    pub end: Option<f64>,
}

/// Whether a play reached the end of its work. A work with no duration is
/// never finished, because nothing says how long it is.
///
/// The two amounts above estimate where the credits start, and credits do
/// not scale with the runtime. So where the catalog holds the operator's
/// answer for the file's credits, the work counts as finished from the
/// start of those credits, whatever time is left. [`credits_line`] reads
/// that start.
pub fn finished(position: i64, duration: i64, credits: &[Credits]) -> bool {
    if duration <= 0 {
        return false;
    }
    match credits_line(duration, credits) {
        Some(line) => position as f64 >= line,
        None => position >= duration - (duration * WATCHED_PERCENT / 100).min(WATCHED_SECONDS),
    }
}

/// The start of the credits, or nothing when the work has none. The
/// operator writes one credits span for a file, in the second half of the
/// work, and the catalog read gives that span alone, so the rule ranks and
/// merges nothing. `watched.go` reads the spans of a Play, which another
/// client can write with several, so both sides take the earliest span that
/// starts in the second half: a credits span in the first half is an
/// opening title sequence and moves nothing. A span with a negative edge is
/// dropped, and one that ends where it starts, or before, marks nothing.
/// [`finished`] calls it only for a positive duration, so the half of the
/// work is always a real position.
fn credits_line(duration: i64, credits: &[Credits]) -> Option<f64> {
    let length = duration as f64;
    credits
        .iter()
        .map(|span| (span.start.unwrap_or(0.0), span.end.unwrap_or(length)))
        .filter(|&(start, end)| start >= 0.0 && end > start && start >= length / 2.0)
        .map(|(start, _)| start)
        .min_by(f64::total_cmp)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    // The cases `library-operator`'s Go test reads too, so a change to the
    // rule on one side fails the other side's test.
    const CASES: &str = include_str!("watched.json");

    fn edge(span: &Value, name: &str) -> Option<f64> {
        span.get(name).map(|value| value.as_f64().unwrap())
    }

    fn credits(case: &Value) -> Vec<Credits> {
        case["credits"]
            .as_array()
            .unwrap()
            .iter()
            .map(|span| Credits {
                start: edge(span, "start"),
                end: edge(span, "end"),
            })
            .collect()
    }

    #[test]
    fn every_shared_case_reads_as_the_go_rule_reads_it() {
        let cases: Vec<Value> = serde_json::from_str(CASES).unwrap();
        let wrong: Vec<&str> = cases
            .iter()
            .filter(|case| {
                let position = case["position"].as_i64().unwrap();
                let duration = case["duration"].as_i64().unwrap();
                let watched = case["watched"].as_bool().unwrap();
                finished(position, duration, &credits(case)) != watched
            })
            .map(|case| case["name"].as_str().unwrap())
            .collect();
        assert!(!cases.is_empty());
        assert_eq!(wrong, Vec::<&str>::new());
    }
}
