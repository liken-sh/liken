//! The list under the sketch of an elevation of a pattern of the
//! catalog: each line of what the pattern expects, with what the cluster
//! says about it. A line that is met is plain with a check. A line that
//! is unmet is marked, and the part that it names is marked in the
//! drawing. A line with no signal says what is missing, and a line that
//! does not apply is dimmed and says why. Each line shows its source
//! when the reader points at it.

use transect_graph::edges::Object;
use transect_graph::links::{Linker, Piece, Reference};
use transect_resources::knowledge::catalog::{Line, Said};

/// One line of what the pattern expects, as the page writes it.
#[derive(Debug, PartialEq)]
pub struct Expected {
    /// The sign before the line, such as a check.
    pub sign: &'static str,
    /// What the cluster says, as a word for a screen reader.
    pub spoken: &'static str,
    /// The class of the line, which says how it is drawn.
    pub class: &'static str,
    /// "writes reach only the primary: orders-rw reaches orders-1", with
    /// each object that has a page linked to it.
    pub text: Vec<Reference>,
    /// The fields that the line read, and that the catalog states it.
    pub source: String,
}

impl Expected {
    /// `line`, with each object that it names linked to the page that
    /// `page` gives.
    pub fn of(line: &Line, page: &dyn Fn(&Object) -> Option<String>) -> Self {
        let (sign, spoken, class) = match line.said {
            Said::Met => ("✓", "met", "sketch-expected"),
            Said::Unmet => ("✗", "unmet", "sketch-expected sketch-expected-unmet"),
            Said::NoSignal => ("?", "no signal", "sketch-expected sketch-expected-silent"),
            Said::Moot => (
                "–",
                "does not apply",
                "sketch-expected sketch-expected-moot",
            ),
        };
        Self {
            sign,
            spoken,
            class,
            text: text(line, page),
            source: line.source.clone(),
        }
    }
}

/// "<expects>: <detail>" as words and links.
fn text(line: &Line, page: &dyn Fn(&Object) -> Option<String>) -> Vec<Reference> {
    let named = line.detail.iter().filter_map(|piece| match piece {
        Piece::Name(object, _) => Some(object.clone()),
        _ => None,
    });
    let linker = Linker::new(named, page);
    let mut pieces = vec![Piece::words(format!("{}: ", line.expects))];
    pieces.extend(line.detail.iter().cloned());
    let parts = linker.link(&pieces).into_iter();
    parts
        .map(|part| Reference {
            text: part.text,
            href: part.href,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::met(Said::Met, ("✓", "met", "sketch-expected"))]
    #[case::unmet(Said::Unmet, ("✗", "unmet", "sketch-expected sketch-expected-unmet"))]
    #[case::no_signal(Said::NoSignal, ("?", "no signal", "sketch-expected sketch-expected-silent"))]
    #[case::moot(Said::Moot, ("–", "does not apply", "sketch-expected sketch-expected-moot"))]
    fn draws_a_line_as_the_cluster_says(#[case] said: Said, #[case] expected: (&str, &str, &str)) {
        let service = Object::new("", "Service", Some("books"), "orders-rw");
        let pod = Object::new("", "Pod", Some("books"), "orders-1");
        let line = Line {
            expects: "writes reach only the primary".into(),
            said,
            detail: vec![
                Piece::Name(service, "orders-rw".into()),
                Piece::words(" reaches "),
                Piece::Name(pod, "orders-1".into()),
            ],
            source: "from {name}-rw".into(),
        };
        let page =
            |object: &Object| (object.kind == "Pod").then(|| format!("/pods/{}", object.name));
        let drawn = Expected::of(&line, &page);
        let text: Vec<(&str, Option<&str>)> = drawn
            .text
            .iter()
            .map(|part| (part.text.as_str(), part.href.as_deref()))
            .collect();
        assert_eq!(
            (drawn.sign, drawn.spoken, drawn.class, text),
            (
                expected.0,
                expected.1,
                expected.2,
                vec![
                    ("writes reach only the primary: orders-rw reaches ", None),
                    ("orders-1", Some("/pods/orders-1"))
                ]
            )
        );
    }
}
