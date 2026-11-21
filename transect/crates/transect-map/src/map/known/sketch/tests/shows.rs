//! What a part shows of the page's own object: the first column of each
//! entry of its role's `shows` that has a value, and nothing on a part
//! that names no object.

use rstest::rstest;

use super::{ELEVATION, REFERENCES, ROLES, page_of};
use crate::map::known::{Sketch, Standing};
use crate::shapes::text::Part;

/// The sketch of the kiosk whose amp role has the `shows` that `shows`
/// writes in YAML, with every named object standing as `standing`.
fn showing(shows: &str, standing: Standing) -> Sketch {
    let roles = ROLES.replace(
        "reads: [Model, Power]}",
        &format!("reads: [Model, Power], shows: {shows}}}"),
    );
    let spec = format!("{{{REFERENCES}, {roles}, {ELEVATION}}}");
    page_of(&spec, standing).sketch.unwrap()
}

fn shown(part: &Part) -> Vec<(&str, &str)> {
    let shows = part.shows.iter();
    shows
        .map(|s| (s.reading.name.as_str(), s.reading.value.as_str()))
        .collect()
}

#[rstest]
#[case::one_column("[Mode]", vec![("Mode", "attract")])]
#[case::the_first_alternative_with_a_value("[[Mode, Stock]]", vec![("Mode", "attract")])]
#[case::the_next_when_the_first_is_empty("[[Sale, Stock]]", vec![("Stock", "full")])]
#[case::nothing_when_every_alternative_is_empty("[[Sale, Gone]]", vec![])]
#[case::each_entry_in_order("[Stock, [Sale, Mode]]", vec![("Stock", "full"), ("Mode", "attract")])]
fn shows_the_columns_of_the_pages_own_object(
    #[case] shows: &str,
    #[case] expected: Vec<(&str, &str)>,
) {
    let sketch = showing(shows, Standing::Plain);
    assert_eq!(shown(&sketch.plan.roles[1].drawn.parts[0]), expected);
}

#[rstest]
#[case::one_column("[Mode]", vec![false])]
#[case::the_first_alternative("[[Mode, Stock]]", vec![false])]
#[case::a_later_alternative("[[Sale, Stock]]", vec![true])]
#[case::each_entry("[Stock, [Sale, Mode]]", vec![false, true])]
fn marks_a_column_that_is_not_the_first_of_its_entry_as_a_fallback(
    #[case] shows: &str,
    #[case] fallbacks: Vec<bool>,
) {
    let sketch = showing(shows, Standing::Plain);
    let shown = sketch.plan.roles[1].drawn.parts[0].shows.iter();
    assert_eq!(shown.map(|s| s.fallback).collect::<Vec<_>>(), fallbacks);
}

#[rstest]
fn shows_nothing_of_the_pages_own_object_on_a_value() {
    let roles = ROLES.replace(
        "shape: screen, expected: true}",
        "shape: screen, expected: true, shows: [Mode]}",
    );
    let spec = format!("{{{REFERENCES}, {roles}, {ELEVATION}}}");
    let sketch = page_of(&spec, Standing::Plain).sketch.unwrap();
    assert_eq!(shown(&sketch.plan.roles[0].drawn.parts[0]), vec![]);
}

#[rstest]
fn shows_nothing_of_the_pages_own_object_on_a_name_with_no_object() {
    let sketch = showing("[Mode]", Standing::Missing);
    assert_eq!(shown(&sketch.plan.roles[1].drawn.parts[0]), vec![]);
}
