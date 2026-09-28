// The words every shape of a member's runs reads as.

use super::*;

#[test]
fn a_run_of_whole_seasons_reads_as_seasons() {
    let cases = [
        (vec![], ""),
        (vec![(3, 0)], "Season 3"),
        (vec![(1, 0), (2, 0)], "Seasons 1 to 2"),
        (vec![(1, 0), (2, 0), (3, 0), (4, 0)], "Seasons 1 to 4"),
        (vec![(1, 0), (3, 0)], "Seasons 1, 3"),
        (vec![(1, 0), (2, 0), (4, 0)], "Seasons 1 to 2, 4"),
    ];
    for (runs, words) in cases {
        assert_eq!(run_words(&runs), words, "{runs:?}");
    }
}

#[test]
fn episodes_inside_one_season_read_as_numbers_under_the_season() {
    let cases = [
        (vec![(1, 8)], "S01 · E08"),
        (
            vec![
                (4, 1),
                (4, 2),
                (4, 3),
                (4, 4),
                (4, 5),
                (4, 6),
                (4, 7),
                (4, 8),
                (4, 9),
                (4, 10),
                (4, 11),
            ],
            "S04 · E01 to E11",
        ),
        (vec![(1, 6), (1, 7)], "S01 · E06, E07"),
        (
            vec![(1, 1), (1, 2), (1, 3), (1, 7)],
            "S01 · E01 to E03, E07",
        ),
    ];
    for (runs, words) in cases {
        assert_eq!(run_words(&runs), words, "{runs:?}");
    }
}

#[test]
fn more_than_one_group_joins_with_dots() {
    let cases = [
        (
            vec![(1, 0), (2, 0), (3, 5), (3, 6)],
            "Seasons 1 to 2 · S03 · E05, E06",
        ),
        (vec![(1, 2), (2, 3)], "S01 · E02 · S02 · E03"),
        (vec![(3, 5), (3, 6), (5, 0)], "S03 · E05, E06 · Season 5"),
    ];
    for (runs, words) in cases {
        assert_eq!(run_words(&runs), words, "{runs:?}");
    }
}
