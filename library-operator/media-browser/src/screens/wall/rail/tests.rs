use super::*;
use crate::catalog::{Fold, Order as Column, Slot, Title};

const LIBRARY: &str = "screening/films";
const GENRE: &str = "Western";

// A wall of titles as one read answers it: the title and the
// release of each, in the order the read gave them.
fn wall(titles: &[(String, String)]) -> Vec<Item> {
    let query = library(Sort::Title);
    titles
        .iter()
        .enumerate()
        .map(|(number, (title, released))| {
            Item::of(
                &query,
                Slot::of(
                    LIBRARY,
                    "movies",
                    Title {
                        id: format!("movies:{number}"),
                        title: title.clone(),
                        released: released.clone(),
                        ..Title::default()
                    },
                ),
            )
        })
        .collect()
}

// This many titles of each release, in the order the pairs are
// given, every one under one name.
fn dated_wall(runs: &[(&str, usize)]) -> Vec<Item> {
    let titles: Vec<(String, String)> = runs
        .iter()
        .flat_map(|(released, count)| {
            (0..*count).map(move |_| ("Specimen".to_string(), (*released).to_string()))
        })
        .collect();
    wall(&titles)
}

// This many titles under each of these first words, in the order
// the pairs are given, every one of one release.
fn titled_wall(runs: &[(&str, usize)]) -> Vec<Item> {
    let titles: Vec<(String, String)> = runs
        .iter()
        .flat_map(|(title, count)| {
            (0..*count).map(move |_| ((*title).to_string(), "1980".to_string()))
        })
        .collect();
    wall(&titles)
}

fn library(sort: Sort) -> Query {
    Query::Library {
        library: LIBRARY.into(),
        sort,
    }
}

fn genre(sort: GenreSort) -> Query {
    Query::Genre {
        name: GENRE.into(),
        order: Column::Released,
        sort,
    }
}

fn labels(bars: &[Jump]) -> Vec<&str> {
    bars.iter().map(|jump| jump.bar.label.as_str()).collect()
}

// The shortest region whose rail holds this many bars of this
// label under this query's sort button, so a case names the room the
// rail has and not a height in pixels.
fn room(bars: usize, label: &str, query: &Query) -> Rectangle {
    (1..)
        .map(|height| area(0.0, 0.0, 0.0, height as f32))
        .find(|region| rail::fits(under_button(*region, query), label) >= bars)
        .expect("some region holds this many bars")
}

#[test]
fn a_wall_of_four_screens_or_fewer_draws_no_rail() {
    assert!(!shows(8));
    assert!(shows(9));
    assert_eq!(rows(48), 8);
    assert_eq!(rows(49), 9);
    let whole = region();
    assert!(bars(&dated_wall(&[("1999", 48)]), &library(Sort::Newest), whole).is_empty());
    assert!(!bars(&dated_wall(&[("1999", 49)]), &library(Sort::Newest), whole).is_empty());
}

#[test]
fn a_wall_with_no_button_and_no_order_draws_no_bars() {
    let items = dated_wall(&[("1999", 60)]);
    let person = Query::Person {
        library: LIBRARY.into(),
        path: ".contributors/A Player".into(),
    };
    assert!(bars(&items, &person, region()).is_empty());
    assert!(!bars(&items, &Query::Released { fold: Fold::Titles }, region()).is_empty());
}

#[test]
fn a_wall_in_release_order_names_years_then_decades_then_ranges() {
    let cases = [
        (
            "years fit",
            vec![
                ("2000", 12),
                ("1999", 12),
                ("1998", 12),
                ("1997", 12),
                ("1996", 12),
            ],
            (5, "1996"),
            vec!["2000", "1999", "1998", "1997", "1996"],
        ),
        (
            "years do not fit, decades do",
            vec![
                ("2005", 6),
                ("2004", 6),
                ("2003", 6),
                ("1995", 6),
                ("1994", 6),
                ("1993", 6),
                ("1985", 6),
                ("1984", 6),
                ("1983", 6),
            ],
            (3, "1980s"),
            vec!["2000s", "1990s", "1980s"],
        ),
        (
            "decades do not fit either",
            vec![
                ("2025", 7),
                ("2015", 7),
                ("2005", 7),
                ("1995", 7),
                ("1985", 7),
                ("1975", 7),
                ("1965", 7),
                ("1955", 7),
            ],
            (3, "1970s\u{2013}1950s"),
            vec![
                "2020s\u{2013}2010s",
                "2000s\u{2013}1980s",
                "1970s\u{2013}1950s",
            ],
        ),
    ];

    for (name, runs, (count, label), want) in cases {
        let query = library(Sort::Newest);
        let items = dated_wall(&runs);
        let region = room(count, label, &query);
        assert_eq!(labels(&bars(&items, &query, region)), want, "{name}");
    }
}

#[test]
fn a_wall_in_title_order_names_letters_then_ranges() {
    let cases = [
        (
            "letters fit",
            vec![("Alpha", 20), ("Bravo", 20), ("Charlie", 20)],
            (3, "A"),
            vec!["A", "B", "C"],
        ),
        (
            "letters do not fit",
            vec![
                ("Alpha", 6),
                ("Bravo", 6),
                ("Charlie", 6),
                ("Delta", 6),
                ("Echo", 6),
                ("Foxtrot", 6),
                ("Golf", 6),
                ("Hotel", 6),
                ("India", 6),
                ("Juliett", 6),
            ],
            (3, "A\u{2013}C"),
            vec!["A\u{2013}C", "D\u{2013}F", "G\u{2013}J"],
        ),
        (
            "an article and a digit",
            vec![("2001 Specimen", 20), ("The Alpha", 20), ("Bravo", 20)],
            (3, "A"),
            vec!["#", "A", "B"],
        ),
    ];

    for (name, runs, (count, label), want) in cases {
        let query = library(Sort::Title);
        let items = titled_wall(&runs);
        let region = room(count, label, &query);
        assert_eq!(labels(&bars(&items, &query, region)), want, "{name}");
    }
}

#[test]
fn a_unit_of_less_than_one_row_folds_into_its_neighbour() {
    let cases = [
        (
            "a thin unit inside the wall",
            vec![("1999", 30), ("1998", 3), ("1997", 30)],
            vec!["1999", "1997"],
        ),
        (
            "a thin unit at the head of the wall",
            vec![("2000", 3), ("1999", 30), ("1998", 30)],
            vec!["1999", "1998"],
        ),
        (
            "a thin unit at the foot of the wall",
            vec![("1999", 30), ("1998", 30), ("1997", 3)],
            vec!["1999", "1998"],
        ),
        ("a wall with no dates at all", vec![("", 60)], vec![""]),
    ];

    for (name, runs, want) in cases {
        let query = library(Sort::Newest);
        let items = dated_wall(&runs);
        let region = room(2, "1999", &query);
        assert_eq!(labels(&bars(&items, &query, region)), want, "{name}");
    }
}

#[test]
fn a_genre_wall_in_its_leading_order_draws_two_bars() {
    let items = dated_wall(&[("1980", 60), ("1990", 12)]);
    let bars = bars(&items, &genre(GenreSort::Leads), region());

    assert_eq!(labels(&bars), ["primarily Western", "other Western"]);
    assert_eq!(bars[0].bar.first, 0);
    assert_eq!(bars[0].bar.last, 9);
    assert_eq!(bars[0].item, 0);
    assert_eq!(bars[1].bar.first, 10);
    assert_eq!(bars[1].bar.last, 11);
    assert_eq!(bars[1].item, 60);
}

#[test]
fn a_genre_wall_whose_titles_all_lead_with_it_draws_one_bar() {
    let items = dated_wall(&[("1990", 30), ("1980", 30)]);
    assert_eq!(
        labels(&bars(&items, &genre(GenreSort::Leads), region())),
        ["primarily Western"]
    );
}

#[test]
fn a_genre_wall_in_a_plain_order_names_that_order() {
    let items = titled_wall(&[("Alpha", 30), ("Bravo", 30)]);
    assert_eq!(
        labels(&bars(
            &items,
            &genre(GenreSort::By(Sort::Title)),
            room(2, "A", &library(Sort::Title))
        )),
        ["A", "B"]
    );
}

#[test]
fn every_bar_covers_a_stretch_of_rows_and_the_bars_cover_them_all() {
    let cases = [
        (
            "years",
            dated_wall(&[("2000", 12), ("1999", 20), ("1998", 31)]),
            library(Sort::Newest),
        ),
        (
            "letters",
            titled_wall(&[("Alpha", 13), ("Bravo", 21), ("Charlie", 29)]),
            library(Sort::Title),
        ),
        (
            "the leading order",
            dated_wall(&[("1980", 55), ("1990", 17)]),
            genre(GenreSort::Leads),
        ),
    ];

    for (name, items, query) in cases {
        let bars = bars(&items, &query, region());
        let last = rows(items.len()) - 1;
        assert_eq!(bars.first().map(|jump| jump.bar.first), Some(0), "{name}");
        assert_eq!(bars.last().map(|jump| jump.bar.last), Some(last), "{name}");
        let starts: Vec<usize> = bars.iter().map(|jump| jump.bar.first).collect();
        let ends: Vec<usize> = bars.iter().map(|jump| jump.bar.last + 1).collect();
        assert_eq!(starts[1..], ends[..ends.len() - 1], "{name}");
    }
}

#[test]
fn a_bar_that_starts_inside_a_row_holds_its_own_first_item() {
    let query = library(Sort::Title);
    let items = titled_wall(&[("Alpha", 20), ("Bravo", 20), ("Charlie", 20)]);
    let bars = bars(&items, &query, room(3, "A", &query));

    assert_eq!(labels(&bars), ["A", "B", "C"]);
    assert_eq!(bars[1].item, 20);
    assert_eq!(bars[1].bar.first, 3);
    assert_eq!(bars[2].item, 40);
    assert_eq!(bars[2].bar.first, 6);
}

#[test]
fn the_button_takes_one_bar_off_the_rail() {
    let sorted = library(Sort::Title);
    let recent = Query::Released { fold: Fold::Titles };
    let whole = region();

    let cell = button(whole, &sorted);
    let under = under_button(whole, &sorted);
    assert!(cell.height > 0.0);
    assert!(
        under.y > whole.y + cell.height,
        "the gap stands between them"
    );
    assert_eq!(under.y + under.height, whole.y + whole.height);
    assert!(rail::fits(under, "Title") < rail::fits(whole, "Title"));

    assert_eq!(button(whole, &recent).height, 0.0);
    assert_eq!(under_button(whole, &recent), whole);
}

#[test]
fn a_longer_label_leaves_room_for_fewer_bars() {
    let query = library(Sort::Newest);
    let runs: Vec<(&str, usize)> = ["2007", "2006", "2005", "2004", "2003", "2002"]
        .into_iter()
        .map(|year| (year, 10))
        .collect();
    let items = dated_wall(&runs);

    assert_eq!(
        labels(&bars(&items, &query, room(6, "2002", &query))).len(),
        6
    );
    assert_eq!(
        labels(&bars(&items, &query, room(2, "2000s", &query))),
        ["2000s"]
    );
}
