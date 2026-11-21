//! Writes the CSS of a shape's lengths, places, and corners from the
//! numbers that the parser read. No text of a definition reaches a style
//! attribute: each value here is a number formatted by Rust or a fixed
//! word.
//!
//! A design unit is `var(--u)`, which the part sets to its own width
//! divided by the design width. So every piece scales with the part by
//! one factor, and the text does not scale.

use transect_resources::shapes::{Length, Place, Round, Span, Term, Unit};

/// One term: a percentage as itself, and design units times `--u`.
fn term(term: &Term) -> String {
    match term.unit {
        Unit::Percent => format!("{}%", term.value),
        Unit::Design => format!("var(--u) * {}", term.value),
    }
}

/// A length as one `calc()`.
pub fn length(length: &Length) -> String {
    let terms: Vec<String> = length.terms().iter().map(term).collect();
    format!("calc({})", terms.join(" + "))
}

/// The properties that put a box at `place` in the part's drawing. A
/// box placed between two edges also states its size, because an `svg`
/// element does not stretch between `left` and `right` as a `div` does.
pub fn place(place: &Place) -> String {
    let across = span(&place.across, ["left", "right", "width"]);
    let down = span(&place.down, ["top", "bottom", "height"]);
    format!("{across}; {down}")
}

fn span(span: &Span, [start, end, size]: [&str; 3]) -> String {
    match span {
        Span::FromStart { start: at, size: s } => {
            format!("{start}: {}; {size}: {}", length(at), length(s))
        }
        Span::FromEnd { end: at, size: s } => {
            format!("{end}: {}; {size}: {}", length(at), length(s))
        }
        Span::Between { start: a, end: b } => format!(
            "{start}: {a}; {end}: {b}; {size}: calc(100% - {a} - {b})",
            a = length(a),
            b = length(b)
        ),
    }
}

/// `border-radius` for `round`: the four horizontal radii, a slash, and
/// the four vertical radii.
pub fn round(round: &Round) -> String {
    let radius = |term: &Term| match term.unit {
        Unit::Percent => format!("{}%", term.value),
        Unit::Design => format!("calc(var(--u) * {})", term.value),
    };
    let across: Vec<String> = round.iter().map(|c| radius(&c.across)).collect();
    let down: Vec<String> = round.iter().map(|c| radius(&c.down)).collect();
    format!("border-radius: {} / {}", across.join(" "), down.join(" "))
}

/// `place` moved `units` design units up and to the right, as a stack
/// draws each copy of its body.
pub fn lifted(place: &Place, units: f64) -> Place {
    let shift = |span: &Span, toward_start: f64| match span {
        Span::FromStart { start, size } => Span::FromStart {
            start: start.plus(toward_start),
            size: size.clone(),
        },
        Span::FromEnd { end, size } => Span::FromEnd {
            end: end.plus(-toward_start),
            size: size.clone(),
        },
        Span::Between { start, end } => Span::Between {
            start: start.plus(toward_start),
            end: end.plus(-toward_start),
        },
    };
    Place {
        across: shift(&place.across, units),
        down: shift(&place.down, -units),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn place_of(text: &str) -> Place {
        Place::read(serde_yaml::from_str(text).unwrap(), "at").unwrap()
    }

    #[rstest]
    #[case::from_the_start(
        "{left: 50% - 16, width: 32, top: 16, height: 32}",
        "left: calc(50% + var(--u) * -16); width: calc(var(--u) * 32); top: calc(var(--u) * 16); height: calc(var(--u) * 32)"
    )]
    #[case::from_the_end(
        "{right: 16, width: 6, bottom: 16, height: 6}",
        "right: calc(var(--u) * 16); width: calc(var(--u) * 6); bottom: calc(var(--u) * 16); height: calc(var(--u) * 6)"
    )]
    #[case::between(
        "{left: 0, right: 0, top: 100% + 1, height: 12}",
        "left: calc(var(--u) * 0); right: calc(var(--u) * 0); width: calc(100% - calc(var(--u) * 0) - calc(var(--u) * 0)); top: calc(100% + var(--u) * 1); height: calc(var(--u) * 12)"
    )]
    fn writes_a_place_from_its_numbers(#[case] text: &str, #[case] css: &str) {
        assert_eq!(place(&place_of(text)), css);
    }

    #[rstest]
    fn writes_elliptical_corners() {
        let read = transect_resources::shapes::length::round(
            Some(&serde_yaml::from_str("[50% / 22, 0, 0, 0]").unwrap()),
            "round",
        )
        .unwrap();
        assert_eq!(
            round(&read),
            "border-radius: 50% calc(var(--u) * 0) calc(var(--u) * 0) calc(var(--u) * 0) / calc(var(--u) * 22) calc(var(--u) * 0) calc(var(--u) * 0) calc(var(--u) * 0)"
        );
    }

    #[rstest]
    fn lifts_a_copy_up_and_to_the_right() {
        let lifted = lifted(&Place::whole(), 8.0);
        assert_eq!(
            place(&lifted),
            "left: calc(var(--u) * 0 + var(--u) * 8); right: calc(var(--u) * 0 + var(--u) * -8); width: calc(100% - calc(var(--u) * 0 + var(--u) * 8) - calc(var(--u) * 0 + var(--u) * -8)); top: calc(var(--u) * 0 + var(--u) * -8); bottom: calc(var(--u) * 0 + var(--u) * 8); height: calc(100% - calc(var(--u) * 0 + var(--u) * -8) - calc(var(--u) * 0 + var(--u) * 8))"
        );
    }
}
