// What the axis of a career holds: its span, its years and their counts,
// its mode, and the marks of the birth and the death.

use super::*;
use crate::screens::person::credit::Seen;
use crate::screens::person::tests::careers::{career, film, seen};

const NOW: i64 = 2026;

fn unborn() -> Life {
    Life::default()
}

fn born(year: &str) -> Life {
    Life::of(year, "", [])
}

#[test]
fn the_span_starts_at_a_birth_or_three_years_before_the_first_credit() {
    let cases = [
        // first, end, birth, span
        (1985, 2026, Some(1970), (1970, 2026)),
        (1985, 2026, None, (1982, 2026)),
        (2000, 2004, None, (1997, 2006)),
        (2000, 2004, Some(1999), (1999, 2008)),
    ];
    for (first, end, birth, held) in cases {
        assert_eq!(span(first, end, birth), held, "{first} {end} {birth:?}");
    }
}

#[test]
fn a_career_with_a_credit_in_the_last_two_years_reaches_now() {
    let recent = Axis::of(&career(&[1990, 1995, 2000, 2010, 2024]), &unborn(), NOW);
    assert_eq!(recent.map(|axis| axis.end), Some(NOW));

    let ended = Axis::of(&career(&[1990, 1995, 2000, 2010, 2023]), &unborn(), NOW);
    assert_eq!(ended.map(|axis| axis.end), Some(2023));
}

#[test]
fn a_short_career_draws_no_axis() {
    let cases: [&[i64]; 3] = [
        &[1990, 1995, 2000, 2010],
        &[2000, 2001, 2002, 2003, 2004],
        &[],
    ];
    for years in cases {
        assert_eq!(Axis::of(&career(years), &unborn(), NOW), None, "{years:?}");
    }
    let five_years = Axis::of(&career(&[2000, 2001, 2002, 2003, 2005]), &unborn(), NOW);
    assert!(five_years.is_some());
}

#[test]
fn undated_credits_take_no_part_in_the_axis() {
    let mut credits = career(&[2000, 2001, 2002, 2003]);
    credits.push(film(9, None, Seen::Nobody));
    assert_eq!(Axis::of(&credits, &unborn(), NOW), None);

    credits.push(film(10, Some(2010), Seen::Nobody));
    let axis = Axis::of(&credits, &unborn(), NOW).expect("five dated credits");
    assert_eq!(axis.year_of(4), None);
    assert_eq!(axis.year_of(5), Some(4));
}

const MARA: &[i64] = &[1985, 1986, 1988, 1991, 1991, 2023];

#[test]
fn an_axis_starts_at_a_birth_no_more_than_25_years_before_the_first_credit() {
    let axis = Axis::of(&career(MARA), &born("1970-06-01"), NOW).expect("an axis");
    assert_eq!((axis.start, axis.left.clone()), (1970, Start::Born));

    let early = Axis::of(&career(MARA), &born("1959"), NOW).expect("an axis");
    assert_eq!(early.start, 1982);
    assert_eq!(early.left, Start::Faded { died: None });
}

#[test]
fn a_death_before_the_axis_labels_the_faded_end() {
    let life = Life::of("1564", "1616", []);
    let axis = Axis::of(&career(&[1991, 1996, 2001, 2011, 2021]), &life, NOW).expect("an axis");
    assert_eq!(
        axis.left,
        Start::Faded {
            died: Some("died 1616".into())
        }
    );
    assert_eq!(axis.death, None);
}

#[test]
fn a_death_inside_the_span_is_a_mark_with_the_age() {
    let life = Life::of("1908-03-02", "1964-09-30", ["1960"]);
    let axis = Axis::of(&career(&[1960, 1961, 1962, 1963, 1990]), &life, NOW).expect("an axis");
    assert_eq!(axis.death, Some((1964, "died 1964, age 56".into())));
    assert_eq!(axis.left, Start::Faded { died: None });
}

#[test]
fn more_than_five_credits_in_one_year_turn_the_whole_axis_into_bars() {
    let dots = Axis::of(
        &career(&[1990, 2000, 2000, 2000, 2000, 2000]),
        &unborn(),
        NOW,
    );
    assert_eq!(
        dots.map(|axis| (axis.mode, axis.tallest)),
        Some((Mode::Dots, 5))
    );

    let years = [1990, 2000, 2000, 2000, 2000, 2000, 2000];
    let bars = Axis::of(&career(&years), &unborn(), NOW);
    assert_eq!(
        bars.map(|axis| (axis.mode, axis.tallest)),
        Some((Mode::Bars, 6))
    );
}

#[test]
fn each_year_counts_what_everyone_and_what_some_have_seen() {
    let credits = seen(&[
        (1990, Seen::Everyone),
        (2000, Seen::Everyone),
        (2000, Seen::Some),
        (2000, Seen::Nobody),
        (2001, Seen::Some),
        (2010, Seen::Nobody),
    ]);
    let axis = Axis::of(&credits, &unborn(), NOW).expect("an axis");
    let counts: Vec<(i64, usize, usize, usize)> = axis
        .years
        .iter()
        .map(|year| (year.year, year.credits.len(), year.everyone, year.some))
        .collect();
    assert_eq!(
        counts,
        [
            (1990, 1, 1, 0),
            (2000, 3, 1, 1),
            (2001, 1, 0, 1),
            (2010, 1, 0, 0)
        ]
    );
    assert_eq!(axis.label(1), "2000 · 3 credits · 1 seen");
    assert_eq!(axis.label(0), "1990 · 1 credit · 1 seen");
    assert_eq!(axis.label(3), "2010 · 1 credit");
    assert_eq!(axis.label(9), "");
}

#[test]
fn the_decade_ticks_fall_inside_the_span() {
    let axis = Axis::of(&career(MARA), &born("1970"), NOW).expect("an axis");
    let ticks: Vec<i64> = axis.ticks().collect();
    assert_eq!(ticks, [1970, 1980, 1990, 2000, 2010, 2020]);

    let late = Axis::of(&career(MARA), &unborn(), NOW).expect("an axis");
    assert_eq!(late.ticks().next(), Some(1990));
}

#[test]
fn a_running_series_runs_from_its_first_year() {
    let mut credits = career(MARA);
    credits[1].run = Some(1986);
    let axis = Axis::of(&credits, &unborn(), NOW).expect("an axis");
    assert_eq!(axis.runs, [1986]);
}
