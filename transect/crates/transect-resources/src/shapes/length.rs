//! The lengths and corners of a shape. An author writes a length as
//! text, such as `50% - 16`, and the page writes it into CSS. So the
//! parser reads the text into numbers, and the page writes `calc()`
//! from those numbers alone: no character of the author's text reaches
//! a style attribute.

use serde_yaml::Value;

/// The largest number, in design units, that a length or a size may
/// have. It keeps every drawing within a size that a page can lay out.
pub const MAX_NUMBER: f64 = 10000.0;

/// The largest percentage that a term may have.
const MAX_PERCENT: f64 = 200.0;

/// What a term of a length counts.
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Unit {
    /// Design units, in which the shape states its size.
    Design,
    /// A share of the part's width for `left`, `right`, and `width`,
    /// and of its height for the others.
    Percent,
}

/// One signed number of a length.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Term {
    pub value: f64,
    pub unit: Unit,
}

/// A sum of terms, such as `50% - 16`. The parser guarantees at least
/// one term.
#[derive(Clone, Debug, PartialEq)]
pub struct Length {
    terms: Vec<Term>,
}

/// One corner's radius: horizontal, then vertical. They are equal
/// unless the author writes `H / V`.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Corner {
    pub across: Term,
    pub down: Term,
}

/// The corners of a body or a piece, in the order that CSS writes them:
/// top-left, top-right, bottom-right, bottom-left.
pub type Round = [Corner; 4];

impl Term {
    pub fn units(value: f64) -> Self {
        Self {
            value,
            unit: Unit::Design,
        }
    }

    /// The term's size at the design size, where a percentage is a
    /// share of `whole`.
    pub fn at(self, whole: f64) -> f64 {
        match self.unit {
            Unit::Design => self.value,
            Unit::Percent => self.value * whole / 100.0,
        }
    }
}

impl Length {
    pub fn units(value: f64) -> Self {
        Self {
            terms: vec![Term::units(value)],
        }
    }

    pub fn terms(&self) -> &[Term] {
        &self.terms
    }

    /// The length with `units` design units added.
    pub fn plus(&self, units: f64) -> Self {
        let mut terms = self.terms.clone();
        terms.push(Term::units(units));
        Self { terms }
    }

    /// The length at the design size, where a percentage is a share of
    /// `whole`.
    pub fn at(&self, whole: f64) -> f64 {
        self.terms.iter().map(|term| term.at(whole)).sum()
    }

    /// The number of design units, when the length is one term of
    /// design units.
    pub fn plain_units(&self) -> Option<f64> {
        match self.terms[..] {
            [
                Term {
                    value,
                    unit: Unit::Design,
                },
            ] => Some(value),
            _ => None,
        }
    }

    /// Reads a YAML number as a length of one term, or text as a sum of
    /// terms. `what` names the field in the error.
    pub fn read(value: &Value, what: &str) -> Result<Self, String> {
        match value {
            Value::Number(_) => Ok(Self::units(number(value, what, -MAX_NUMBER, MAX_NUMBER)?)),
            Value::String(text) => sum(text).ok_or_else(|| {
                format!(
                    "{what} is not a length: a number of design units or a percentage of at most {MAX_PERCENT}%, or a sum of them joined by + and -, each at most {MAX_NUMBER}"
                )
            }),
            _ => Err(format!("{what} is not a length")),
        }
    }
}

impl Corner {
    fn even(term: Term) -> Self {
        Self {
            across: term,
            down: term,
        }
    }

    /// The corner on the other axis, as a quarter turn draws it.
    pub fn turned(self) -> Self {
        Self {
            across: self.down,
            down: self.across,
        }
    }
}

/// A corner with no radius.
pub fn square() -> Round {
    [Corner::even(Term::units(0.0)); 4]
}

/// Reads `round`: one radius for every corner, or a list of four. A
/// radius is one term of zero or more, or `H / V`.
pub fn round(value: Option<&Value>, what: &str) -> Result<Round, String> {
    match value {
        None => Ok(square()),
        Some(Value::Sequence(corners)) => {
            let corners = corners.iter().map(|corner| radius(corner, what));
            let corners = corners.collect::<Result<Vec<Corner>, String>>()?;
            corners
                .try_into()
                .map_err(|_| format!("{what} is one radius or a list of four"))
        }
        Some(one) => Ok([radius(one, what)?; 4]),
    }
}

fn radius(value: &Value, what: &str) -> Result<Corner, String> {
    let fault = || {
        format!(
            "{what} is not a radius: one number or percentage of zero or more, or two of them as H / V"
        )
    };
    let one = |value: &Value| {
        let length = Length::read(value, what).map_err(|_| fault())?;
        match length.terms[..] {
            [term] if term.value >= 0.0 => Ok(term),
            _ => Err(fault()),
        }
    };
    match value {
        Value::String(text) => match text.split_once('/') {
            Some((across, down)) => Ok(Corner {
                across: one(&Value::String(across.into()))?,
                down: one(&Value::String(down.into()))?,
            }),
            None => Ok(Corner::even(one(value)?)),
        },
        _ => Ok(Corner::even(one(value)?)),
    }
}

/// A YAML number from `low` to `high`. YAML can write an infinity or
/// not-a-number, and neither is in any range.
pub fn number(value: &Value, what: &str, low: f64, high: f64) -> Result<f64, String> {
    let found = value
        .as_f64()
        .filter(|n| n.is_finite() && (low..=high).contains(n));
    found.ok_or_else(|| format!("{what} is not a number from {low} to {high}"))
}

/// A percentage such as `62%` from `low` to `high`.
pub fn percent(value: &Value, what: &str, low: f64, high: f64) -> Result<f64, String> {
    let fault = || format!("{what} is not a percentage from {low}% to {high}%");
    let text = value.as_str().ok_or_else(fault)?;
    let digits = text.strip_suffix('%').ok_or_else(fault)?;
    let (found, rest) = decimal(digits).ok_or_else(fault)?;
    match rest.is_empty() && (low..=high).contains(&found) {
        true => Ok(found),
        false => Err(fault()),
    }
}

/// Reads `term (("+" | "-") term)*`, where the first term may have a
/// sign and a term is a decimal number, followed by `%` for a
/// percentage. Spaces may stand around each sign.
fn sum(text: &str) -> Option<Length> {
    let mut terms = Vec::new();
    let mut rest = text.trim_start();
    while !rest.is_empty() {
        let (sign, after) = match rest.chars().next() {
            Some('+') => (1.0, &rest[1..]),
            Some('-') => (-1.0, &rest[1..]),
            _ if terms.is_empty() => (1.0, rest),
            _ => return None,
        };
        let (value, after) = decimal(after.trim_start())?;
        let (unit, after, cap) = match after.strip_prefix('%') {
            Some(after) => (Unit::Percent, after, MAX_PERCENT),
            None => (Unit::Design, after, MAX_NUMBER),
        };
        if value > cap {
            return None;
        }
        terms.push(Term {
            value: sign * value,
            unit,
        });
        rest = after.trim_start();
    }
    (!terms.is_empty()).then_some(Length { terms })
}

/// Reads digits with an optional fraction from the start of `text`, and
/// returns the number and the text after it. An exponent is not part of
/// the grammar, so `1e9` stops at `e`.
fn decimal(text: &str) -> Option<(f64, &str)> {
    let digits = |text: &str| {
        text.find(|c: char| !c.is_ascii_digit())
            .unwrap_or(text.len())
    };
    let whole = digits(text);
    if whole == 0 {
        return None;
    }
    let mut end = whole;
    if let Some(fraction) = text[whole..].strip_prefix('.') {
        let places = digits(fraction);
        if places == 0 {
            return None;
        }
        end = whole + 1 + places;
    }
    let value: f64 = text[..end].parse().ok()?;
    Some((value, &text[end..]))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn yaml(text: &str) -> Value {
        serde_yaml::from_str(text).unwrap()
    }

    fn term(value: f64, unit: Unit) -> Term {
        Term { value, unit }
    }

    #[rstest]
    #[case::a_number("12", vec![term(12.0, Unit::Design)])]
    #[case::a_negative_number("-96", vec![term(-96.0, Unit::Design)])]
    #[case::a_fraction("'4.4'", vec![term(4.4, Unit::Design)])]
    #[case::a_percentage("50%", vec![term(50.0, Unit::Percent)])]
    #[case::a_sum("50% - 16", vec![term(50.0, Unit::Percent), term(-16.0, Unit::Design)])]
    #[case::a_sum_with_no_spaces("100%+1", vec![term(100.0, Unit::Percent), term(1.0, Unit::Design)])]
    #[case::a_signed_first_term("'-5 + 50%'", vec![term(-5.0, Unit::Design), term(50.0, Unit::Percent)])]
    fn reads_a_length_into_terms(#[case] text: &str, #[case] terms: Vec<Term>) {
        assert_eq!(Length::read(&yaml(text), "at.left").unwrap().terms, terms);
    }

    #[rstest]
    #[case::trailing_css("10; background: url(https://x.test/a)")]
    #[case::an_exponent("1e9")]
    #[case::a_unit("10px")]
    #[case::a_number_past_the_cap("10001")]
    #[case::a_percentage_past_the_cap("201%")]
    #[case::a_huge_yaml_number("-100000")]
    #[case::not_a_number(".nan")]
    #[case::an_infinity(".inf")]
    #[case::two_terms_with_no_sign("10 20")]
    #[case::a_sign_with_no_term("'10 -'")]
    #[case::a_bare_fraction("'.5'")]
    #[case::a_point_with_no_digits("'5.'")]
    #[case::empty("''")]
    #[case::a_list("[1, 2]")]
    #[case::a_css_function("calc(1px)")]
    fn refuses_a_length_outside_the_grammar(#[case] text: &str) {
        assert!(Length::read(&yaml(text), "at.left").is_err(), "{text}");
    }

    #[rstest]
    fn names_the_field_of_a_length_it_refuses() {
        let error = Length::read(&yaml("10px"), "pieces[2].at.left").unwrap_err();
        assert!(
            error.starts_with("pieces[2].at.left is not a length"),
            "{error}"
        );
    }

    #[rstest]
    #[case::none(None, [(0.0, 0.0); 4])]
    #[case::one_number(Some("4"), [(4.0, 4.0); 4])]
    #[case::elliptical(Some("50% / 22"), [(50.0, 22.0); 4])]
    #[case::four(Some("[0, 0, 2, 2]"), [(0.0, 0.0), (0.0, 0.0), (2.0, 2.0), (2.0, 2.0)])]
    fn reads_the_corners(#[case] text: Option<&str>, #[case] corners: [(f64, f64); 4]) {
        let value = text.map(yaml);
        let round = round(value.as_ref(), "round").unwrap();
        let read = round.map(|corner| (corner.across.value, corner.down.value));
        assert_eq!(read, corners);
    }

    #[rstest]
    #[case::css_after_a_percentage("'1%; background-image: url(https://x.test/a); --x: 1%'")]
    #[case::a_negative_radius("-4")]
    #[case::a_sum("4 + 2")]
    #[case::three_corners("[1, 2, 3]")]
    #[case::a_bad_vertical_radius("4 / 2px")]
    fn refuses_a_corner_outside_the_grammar(#[case] text: &str) {
        assert!(round(Some(&yaml(text)), "round").is_err(), "{text}");
    }

    #[rstest]
    #[case::in_range("62%", Some(62.0))]
    #[case::css_after_it("1cqi, 9999px); background: url(https://x.test/a", None)]
    #[case::no_sign("62", None)]
    #[case::past_the_top("101%", None)]
    #[case::a_sum("50% + 1%", None)]
    fn reads_a_percentage(#[case] text: &str, #[case] found: Option<f64>) {
        let value = Value::String(text.into());
        assert_eq!(percent(&value, "share", 1.0, 100.0).ok(), found);
    }

    #[rstest]
    #[case::in_range("12", Some(12.0))]
    #[case::below("0", None)]
    #[case::not_a_number(".nan", None)]
    #[case::text("'12'", None)]
    fn reads_a_number_in_a_range(#[case] text: &str, #[case] found: Option<f64>) {
        assert_eq!(number(&yaml(text), "axis", 1.0, 100.0).ok(), found);
    }
}
