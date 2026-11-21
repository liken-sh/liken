//! The conditions of a custom resource, drawn as chips in the object's
//! box at the center of its kind's diagram.  The health condition that
//! the kind's knowledge names, or `Ready`, comes first, and the rest
//! keep their order in `status.conditions`.  Only the health condition
//! gets a colored dot.  The others are gray, because transect cannot
//! tell whether their `True` is good or bad.  The health condition is
//! gray too when it is `False` with one of the kind's calm reasons.

use serde_json::Value;

use transect_graph::untrusted::capped;
use transect_observe::health::{Deciding, Level};

/// Caps the chips, so that an operator that writes many conditions
/// does not push the box's other lines out.
const MAX_CHIPS: usize = 4;

/// The most characters in a chip's text.
const CHIP_CHARS: usize = 40;

/// One condition as a chip: "Screen: present", "Power applied".
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Chip {
    pub text: String,
    /// The condition's message, for the chip's title.
    pub title: Option<String>,
    pub status: Status,
    /// The color of the chip's dot, or `None` for gray.
    pub level: Option<Level>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    True,
    False,
    Unknown,
}

/// The first `MAX_CHIPS` chips of `data`'s `status.conditions`, with
/// the health condition first.
pub fn chips(data: &Value, deciding: &Deciding) -> Vec<Chip> {
    let mut chips = every_chip(data, deciding);
    chips.truncate(MAX_CHIPS);
    chips
}

/// Every chip of `data`'s `status.conditions`, the health condition
/// first.
pub fn every_chip(data: &Value, deciding: &Deciding) -> Vec<Chip> {
    let healthy = deciding.condition.as_deref();
    let conditions = data.pointer("/status/conditions").and_then(Value::as_array);
    let mut found: Vec<(bool, Chip)> = conditions
        .into_iter()
        .flatten()
        .filter_map(|condition| {
            let text = |key: &str| condition.get(key).and_then(Value::as_str);
            let kind = text("type")?;
            let status = match text("status") {
                Some("True") => Status::True,
                Some("False") => Status::False,
                _ => Status::Unknown,
            };
            let level = deciding.level(kind, text("status").unwrap_or("Unknown"), text("reason"));
            let chip = Chip {
                text: capped(&words(kind, text("reason"), status), CHIP_CHARS)?,
                title: text("message").and_then(|message| capped(message, 200)),
                status,
                level,
            };
            Some((Some(kind) != healthy, chip))
        })
        .collect();
    found.sort_by_key(|(later, _)| *later);
    found.into_iter().map(|(_, chip)| chip).collect()
}

/// A condition's chip text: its type split into words, then its reason
/// after a colon, unless the type already contains the reason, as
/// "PowerApplied" contains "Applied".  With no reason to add, a False
/// "Ready" becomes "Not ready" and an Unknown one "Ready unknown".
fn words(kind: &str, reason: Option<&str>, status: Status) -> String {
    let named = spaced(kind);
    let reason = reason
        .map(spaced)
        .filter(|reason| !named.to_lowercase().contains(&reason.to_lowercase()));
    match (reason, status) {
        (Some(reason), _) => format!("{named}: {}", reason.to_lowercase()),
        (None, Status::True) => named,
        (None, Status::False) => format!("Not {}", named.to_lowercase()),
        (None, Status::Unknown) => format!("{named} unknown"),
    }
}

/// "PowerApplied" as "Power applied": the words of a type in camel
/// case, the first as written and each later one in lowercase. A word
/// starts at a capital after a lowercase letter, and at the last capital
/// of a run of capitals that a lowercase letter follows, so an acronym
/// stays whole: "ConsistentSystemID" is "Consistent system ID", and
/// "TLSReady" is "TLS ready".
fn spaced(word: &str) -> String {
    let chars: Vec<char> = word.chars().collect();
    let mut words: Vec<String> = Vec::new();
    for (at, &c) in chars.iter().enumerate() {
        let before = at.checked_sub(1).map(|at| chars[at]);
        let after = chars.get(at + 1);
        let starts = c.is_uppercase()
            && (before.is_some_and(char::is_lowercase)
                || (before.is_some_and(char::is_uppercase)
                    && after.is_some_and(|a| a.is_lowercase())));
        match words.last_mut() {
            Some(last) if !starts => last.push(c),
            _ => words.push(c.to_string()),
        }
    }
    let shown = words.iter().enumerate().map(|(at, word)| {
        let acronym = word.chars().count() > 1 && word.chars().all(|c| !c.is_lowercase());
        match at == 0 || acronym {
            true => word.clone(),
            false => word.to_lowercase(),
        }
    });
    shown.collect::<Vec<String>>().join(" ")
}

impl Chip {
    pub fn dot_class(&self) -> &'static str {
        crate::objects::condition_dot(self.level)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn read(conditions: &str, healthy: Option<&str>) -> Vec<(String, Status)> {
        let data: Value =
            serde_yaml::from_str(&format!("status: {{conditions: {conditions}}}")).unwrap();
        let deciding = Deciding {
            condition: Some(healthy.unwrap_or("Ready").into()),
            calm: vec!["Idle".into()],
        };
        let found = chips(&data, &deciding);
        found
            .into_iter()
            .map(|chip| (chip.text, chip.status))
            .collect()
    }

    #[rstest]
    #[case::the_deciding_one_working("[{type: Ready, status: 'True'}]", "dot dot-clear")]
    #[case::the_deciding_one_failing(
        "[{type: Ready, status: 'False', reason: Jammed}]",
        "dot concern-failing"
    )]
    #[case::the_deciding_one_at_rest(
        "[{type: Ready, status: 'False', reason: Idle}]",
        "dot dot-gray"
    )]
    #[case::another_one_true("[{type: AttentionNeeded, status: 'True'}]", "dot dot-gray")]
    #[case::another_one_false("[{type: Active, status: 'False'}]", "dot dot-gray")]
    fn colors_only_the_deciding_condition(#[case] conditions: &str, #[case] expected: &str) {
        let data: Value =
            serde_yaml::from_str(&format!("status: {{conditions: {conditions}}}")).unwrap();
        let deciding = Deciding {
            condition: Some("Ready".into()),
            calm: vec!["Idle".into()],
        };
        assert_eq!(chips(&data, &deciding)[0].dot_class(), expected);
    }

    #[rstest]
    #[case::two_words("PowerApplied", "Power applied")]
    #[case::one_word("Ready", "Ready")]
    #[case::an_acronym_at_the_end("ConsistentSystemID", "Consistent system ID")]
    #[case::an_acronym_at_the_start("TLSReady", "TLS ready")]
    #[case::three_words("ContinuousArchiving", "Continuous archiving")]
    fn keeps_an_acronym_whole(#[case] kind: &str, #[case] words: &str) {
        assert_eq!(spaced(kind), words);
    }

    fn chip(text: &str, status: Status) -> (String, Status) {
        (text.into(), status)
    }

    #[rstest]
    #[case::the_healthy_condition_first(
        "[{type: PowerApplied, status: 'True', reason: Applied}, {type: Screen, status: 'True', reason: Present}]",
        Some("Screen"),
        vec![chip("Screen: present", Status::True), chip("Power applied", Status::True)]
    )]
    #[case::a_failure_with_no_reason(
        "[{type: Ready, status: 'False'}]",
        None,
        vec![chip("Not ready", Status::False)]
    )]
    #[case::an_unknown("[{type: Synced, status: Unknown, reason: Waiting}]", None, vec![chip("Synced: waiting", Status::Unknown)])]
    #[case::no_conditions("[]", None, vec![])]
    #[case::at_most_four(
        "[{type: A, status: 'True'}, {type: B, status: 'True'}, {type: C, status: 'True'}, {type: D, status: 'True'}, {type: E, status: 'True'}]",
        None,
        vec![chip("A", Status::True), chip("B", Status::True), chip("C", Status::True), chip("D", Status::True)]
    )]
    fn shows_each_condition_as_a_chip(
        #[case] conditions: &str,
        #[case] healthy: Option<&str>,
        #[case] expected: Vec<(String, Status)>,
    ) {
        assert_eq!(read(conditions, healthy), expected);
    }
}
