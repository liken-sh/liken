//! The conditions a custom resource reports, as the chips its box shows
//! in the middle of its kind's diagram.  The condition that its knowledge
//! names as the sign that the object works comes first, then the rest in
//! the order the operator writes them.  Only that first condition takes a
//! color.  The rest, and the first condition when it is at rest, draw
//! gray.

use serde_json::Value;

use transect_graph::untrusted::capped;
use transect_observe::health::{Deciding, Level};

/// The most conditions a box shows, so an operator that writes many
/// does not push the box's other lines out.
const MOST: usize = 4;

/// The longest a chip's words run.
const CHIP_CHARS: usize = 40;

/// One condition as a chip: "Screen: present", "Power applied".
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Chip {
    pub text: String,
    /// The condition's message, for the chip's title.
    pub title: Option<String>,
    pub status: Status,
    /// The level its dot draws, or none for a gray dot.
    pub level: Option<Level>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    True,
    False,
    Unknown,
}

/// The first chips of `data`'s `status.conditions`, the deciding one
/// first, as many as the object's box in the diagram shows.
pub fn chips(data: &Value, deciding: &Deciding) -> Vec<Chip> {
    let mut chips = every_chip(data, deciding);
    chips.truncate(MOST);
    chips
}

/// Every chip of `data`'s `status.conditions`, the deciding one first.
pub fn every_chip(data: &Value, deciding: &Deciding) -> Vec<Chip> {
    let healthy = Some(deciding.condition.as_str());
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

/// A condition in words: its type split where its capitals are, then
/// its reason after a colon, unless the type already contains the
/// reason, as "PowerApplied" contains "Applied".  With no reason to add,
/// a False condition is written "Not ready" and an Unknown one "Ready
/// unknown".
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

/// "PowerApplied" as "Power applied": a space before each capital that
/// follows a lowercase letter, and every word after the first in
/// lowercase.
fn spaced(word: &str) -> String {
    let mut out = String::new();
    let mut previous_lower = false;
    for c in word.chars() {
        if c.is_uppercase() && previous_lower {
            out.push(' ');
            out.extend(c.to_lowercase());
        } else {
            out.push(c);
        }
        previous_lower = c.is_lowercase();
    }
    out
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
            condition: healthy.unwrap_or("Ready").into(),
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
            condition: "Ready".into(),
            calm: vec!["Idle".into()],
        };
        assert_eq!(chips(&data, &deciding)[0].dot_class(), expected);
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
