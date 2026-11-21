//! A role that states `stack` draws its parts that are alike as one
//! part, because "stack what is alike" is a rule of every view: a reader
//! sees how many there are and where each runs without reading each
//! one. A part is alike when it is drawn plainly. A part that its health
//! or an unmet expectation marks stands apart, beside the stack, so the
//! reader sees which one deviates and why.

use crate::shapes::text::{End, Part, Reading, State};
use transect_graph::links::Reference;

/// `parts` with the plain ones drawn as one part, first, and each other
/// part after it, in their order. One plain part is drawn as itself,
/// because a stack of one has no count.
pub fn gather(parts: Vec<Part>) -> Vec<Part> {
    let (alike, apart): (Vec<Part>, Vec<Part>) = parts
        .into_iter()
        .partition(|part| part.state == State::Plain);
    if alike.len() < 2 {
        return alike.into_iter().chain(apart).collect();
    }
    let stack: Vec<Reference> = alike
        .iter()
        .map(|part| part.end.reference.clone())
        .collect();
    let text: Vec<&str> = stack.iter().map(|name| name.text.as_str()).collect();
    let first = &alike[0];
    let stacked = Part {
        end: End {
            reference: Reference {
                text: text.join(", "),
                href: None,
            },
            under: None,
            reaches: Vec::new(),
        },
        readings: readings(&alike),
        shows: first.shows.clone(),
        state: State::Plain,
        literal: false,
        why: None,
        stack,
    };
    std::iter::once(stacked).chain(apart).collect()
}

/// What a stack writes for a part that has no value for a reading, as
/// a table of the page writes a cell with no value, so each value stays
/// in the place of its part's name.
const NO_VALUE: &str = "—";

/// Each reading of the parts, in the order that the parts read them,
/// with one value for each part in the order of the parts' names. A
/// value that every part has is written once, followed by "each", so
/// "ready 1/1 each" says that each part is ready. Only such a value
/// keeps the object it was read from, because the others are several.
fn readings(parts: &[Part]) -> Vec<Reading> {
    let mut names: Vec<&str> = Vec::new();
    for reading in parts.iter().flat_map(|part| &part.readings) {
        if !names.contains(&reading.name.as_str()) {
            names.push(&reading.name);
        }
    }
    let read = |name: &str| {
        let of_each = parts
            .iter()
            .map(|part| part.readings.iter().find(|r| r.name == name));
        let of_each: Vec<Option<&Reading>> = of_each.collect();
        let first = of_each.iter().flatten().next().copied()?;
        let same = of_each
            .iter()
            .all(|r| r.is_some_and(|r| r.value == first.value));
        if same {
            return Some(Reading {
                value: format!("{} each", first.value),
                ..first.clone()
            });
        }
        let values = of_each
            .iter()
            .map(|r| r.map_or(NO_VALUE, |r| r.value.as_str()));
        Some(Reading {
            name: name.into(),
            value: values.collect::<Vec<&str>>().join(", "),
            from: None,
        })
    };
    names.into_iter().filter_map(read).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::shapes::text::Why;

    fn part(name: &str, state: State, readings: &[(&str, &str)]) -> Part {
        Part {
            end: End {
                reference: Reference {
                    text: format!("Pod {name}"),
                    href: Some(format!("/pods/{name}")),
                },
                under: None,
                reaches: Vec::new(),
            },
            readings: readings
                .iter()
                .map(|(name, value)| Reading {
                    name: name.to_string(),
                    value: value.to_string(),
                    from: None,
                })
                .collect(),
            shows: Vec::new(),
            state,
            literal: false,
            why: (state != State::Plain).then(|| Why {
                reason: "not ready".into(),
                message: None,
            }),
            stack: Vec::new(),
        }
    }

    /// Each part as its name, its stack's names, its state, and its
    /// readings.
    type Drawn = (String, Vec<String>, State, Vec<(String, String)>);

    fn drawn(parts: Vec<Part>) -> Vec<Drawn> {
        let one = |part: Part| {
            let stack = part.stack.into_iter().map(|name| name.text).collect();
            let readings = part
                .readings
                .into_iter()
                .map(|r| (r.name, r.value))
                .collect();
            (part.end.reference.text, stack, part.state, readings)
        };
        parts.into_iter().map(one).collect()
    }

    fn owned(name: &str, stack: &[&str], state: State, readings: &[(&str, &str)]) -> Drawn {
        (
            name.into(),
            stack.iter().map(|s| s.to_string()).collect(),
            state,
            readings
                .iter()
                .map(|(n, v)| (n.to_string(), v.to_string()))
                .collect(),
        )
    }

    #[rstest]
    #[case::alike_parts_as_one(
        vec![
            part("a", State::Plain, &[("ready", "1/1"), ("node", "alpha")]),
            part("b", State::Plain, &[("ready", "1/1"), ("node", "beta")]),
        ],
        vec![owned("Pod a, Pod b", &["Pod a", "Pod b"], State::Plain, &[("ready", "1/1 each"), ("node", "alpha, beta")])]
    )]
    #[case::a_part_that_its_health_marks_apart(
        vec![
            part("a", State::Plain, &[("node", "alpha")]),
            part("b", State::Warning, &[("node", "beta")]),
            part("c", State::Plain, &[("node", "gamma")]),
        ],
        vec![
            owned("Pod a, Pod c", &["Pod a", "Pod c"], State::Plain, &[("node", "alpha, gamma")]),
            owned("Pod b", &[], State::Warning, &[("node", "beta")]),
        ]
    )]
    #[case::one_alike_part_as_itself(
        vec![part("a", State::Plain, &[("node", "alpha")]), part("b", State::Failing, &[])],
        vec![owned("Pod a", &[], State::Plain, &[("node", "alpha")]), owned("Pod b", &[], State::Failing, &[])]
    )]
    #[case::values_in_the_order_of_the_names(
        vec![part("a", State::Plain, &[("node", "zeta")]), part("b", State::Plain, &[("node", "alpha")])],
        vec![owned("Pod a, Pod b", &["Pod a", "Pod b"], State::Plain, &[("node", "zeta, alpha")])]
    )]
    #[case::a_reading_that_one_part_lacks(
        vec![part("a", State::Plain, &[("claim", "data-a 1Gi")]), part("b", State::Plain, &[])],
        vec![owned("Pod a, Pod b", &["Pod a", "Pod b"], State::Plain, &[("claim", "data-a 1Gi, —")])]
    )]
    fn stacks_the_parts_that_are_alike(#[case] parts: Vec<Part>, #[case] expected: Vec<Drawn>) {
        assert_eq!(drawn(gather(parts)), expected);
    }
}
