use super::*;

const LIBRARY: &str = "screening/serials";

fn movie(id: &str, released: &str) -> Candidate {
    Candidate::Movie {
        slot: Slot::of(
            "screening/films",
            "movies",
            Title {
                id: id.into(),
                title: id.into(),
                released: released.into(),
                art: format!("{id}.jpg"),
                ..Title::default()
            },
        ),
    }
}

fn serial() -> Title {
    Title {
        id: "series:1".into(),
        title: "The Serial".into(),
        released: "2004".into(),
        art: "serial.jpg".into(),
        rating: "TV-14".into(),
        ..Title::default()
    }
}

// One episode of the serial, released on the date and arrived this
// many days after it.
fn episode(number: i64, released: &str, days_after: i64) -> Candidate {
    Candidate::Episode {
        library: LIBRARY.into(),
        episode: Title {
            id: format!("episode:{number}"),
            title: format!("Segment {number}"),
            released: released.into(),
            art: format!("still-{number}.jpg"),
            duration: 2_760,
            ..Title::default()
        },
        added: date_seconds(released).unwrap_or(0) + days_after * DAY,
        season: 3,
        number,
        series: serial(),
    }
}

fn ids(slots: &[Slot]) -> Vec<&str> {
    slots.iter().map(|slot| slot.id.as_str()).collect()
}

#[test]
fn a_date_inside_the_window_of_today_is_current_and_a_year_alone_never_is() {
    let today = date_seconds("2026-09-03").expect("a full date");
    for (released, expected) in [
        ("2026-09-03", true),
        ("2026-08-04", true),
        ("2026-08-03", false),
        ("2026-09-10", true),
        ("2026-10-04", false),
        ("2026", false),
        ("", false),
    ] {
        assert_eq!(current(released, today), expected, "{released}");
    }
}

#[test]
fn a_full_date_reads_as_seconds_and_a_year_alone_reads_as_nothing() {
    assert_eq!(date_seconds("1970-01-01"), Some(0));
    assert_eq!(date_seconds("2000-03-01"), Some(951_868_800));
    assert_eq!(date_seconds("2024-02-29"), Some(1_709_164_800));
    assert_eq!(date_seconds("2004"), None);
    assert_eq!(date_seconds(""), None);
    assert_eq!(date_seconds("2004-13-01"), None);
    assert_eq!(date_seconds("soon-01-01"), None);
}

#[test]
fn the_titles_fold_makes_one_series_slot_of_a_season_drop() {
    let slots = fold(
        vec![
            episode(3, "2026-09-03", 1),
            episode(2, "2026-08-27", 1),
            movie("movie:1", "2026-08-20"),
            episode(1, "2026-08-20", 1),
        ],
        Fold::Titles,
    );
    assert_eq!(ids(&slots), ["series:1", "movie:1"]);
    assert_eq!(slots[0].kind, "series");
    assert_eq!(slots[0].library, LIBRARY);
    assert_eq!(slots[0].title, "The Serial");
    assert_eq!(slots[0].released, "2026-09-03");
    assert_eq!(slots[0].art, "serial.jpg");
    assert_eq!(slots[0].rating, "TV-14");
    assert!(!slots[0].still());
}

#[test]
fn the_episodes_fold_keeps_every_episode_as_a_still() {
    let slots = fold(
        vec![episode(2, "2026-08-27", 400), episode(1, "2026-08-20", 400)],
        Fold::Episodes,
    );
    assert_eq!(ids(&slots), ["episode:2", "episode:1"]);
    assert!(slots.iter().all(Slot::still));
    assert_eq!(slots[0].kind, "episodes");
    assert_eq!(slots[0].title, "Segment 2");
    assert_eq!(slots[0].art, "still-2.jpg");
    assert_eq!(slots[0].duration, 2_760);
    assert_eq!(
        slots[0].episode,
        Some(InSeries {
            series: "series:1".into(),
            name: "The Serial".into(),
            season: 3,
            episode: 2,
        })
    );
}

#[test]
fn the_airing_fold_keeps_an_episode_inside_the_window_and_folds_the_rest() {
    let slots = fold(
        vec![
            episode(4, "2026-09-03", WINDOW_DAYS),
            episode(3, "2026-08-27", -2),
            episode(2, "2026-08-20", WINDOW_DAYS + 1),
            episode(1, "2026-08-13", 400),
        ],
        Fold::Airing,
    );
    assert_eq!(ids(&slots), ["episode:4", "episode:3", "series:1"]);
    assert_eq!(slots[2].released, "2026-08-20");
}

fn today() -> i64 {
    date_seconds("2026-09-03").expect("a full date")
}

fn shown(candidates: Vec<Candidate>) -> Vec<Slot> {
    fold(candidates, Fold::Shows { today: today() })
}

#[test]
fn the_shows_fold_makes_one_still_of_the_newest_episode_of_a_series() {
    let slots = shown(vec![
        episode(8, "2026-09-03", 1),
        episode(7, "2026-08-27", 1),
        episode(1, "2026-03-01", 400),
    ]);
    assert_eq!(ids(&slots), ["series:1"]);
    assert_eq!(slots[0].kind, "episodes");
    assert_eq!(slots[0].library, LIBRARY);
    assert_eq!(slots[0].title, "Segment 8");
    assert_eq!(slots[0].art, "still-8.jpg");
    assert_eq!(slots[0].released, "2026-09-03");
    assert_eq!(slots[0].duration, 2_760);
    assert_eq!(slots[0].new, 2);
    assert!(slots[0].still());
    assert!(slots[0].folded());
    assert_eq!(
        slots[0].episode,
        Some(InSeries {
            series: "series:1".into(),
            name: "The Serial".into(),
            season: 3,
            episode: 8,
        })
    );
}

#[test]
fn the_newest_episode_takes_the_slot_whatever_order_they_came_in() {
    let slots = shown(vec![
        episode(7, "2026-08-27", 1),
        episode(8, "2026-09-03", 1),
    ]);
    assert_eq!(slots[0].art, "still-8.jpg");
    assert_eq!(slots[0].released, "2026-09-03");
    assert_eq!(slots[0].new, 2);
}

#[test]
fn two_episodes_of_one_date_break_the_tie_by_their_numbers() {
    let slots = shown(vec![
        episode(7, "2026-09-03", 1),
        episode(8, "2026-09-03", 1),
    ]);
    assert_eq!(slots[0].art, "still-8.jpg");
}

#[test]
fn a_series_with_one_current_episode_is_one_new() {
    let slots = shown(vec![
        episode(8, "2026-09-03", 1),
        episode(1, "2026-03-01", 400),
    ]);
    assert_eq!(slots[0].new, 1);
    assert_eq!(slots[0].art, "still-8.jpg");
}

#[test]
fn a_series_of_old_episodes_alone_is_none_new_and_still_draws_as_a_still() {
    let slots = shown(vec![
        episode(2, "2026-03-01", 400),
        episode(1, "2026-02-01", 400),
    ]);
    assert_eq!(ids(&slots), ["series:1"]);
    assert_eq!(slots[0].new, 0);
    assert_eq!(slots[0].art, "still-2.jpg");
    assert!(slots[0].still());
}

// One episode of a second serial, released on the date and arrived
// the same day.
fn other(number: i64, released: &str) -> Candidate {
    Candidate::Episode {
        library: LIBRARY.into(),
        episode: Title {
            id: format!("part:{number}"),
            title: format!("Part {number}"),
            released: released.into(),
            art: format!("part-{number}.jpg"),
            duration: 1_800,
            ..Title::default()
        },
        added: date_seconds(released).unwrap_or(0),
        season: 1,
        number,
        series: Title {
            id: "series:2".into(),
            title: "The Other".into(),
            released: "2019".into(),
            art: "other.jpg".into(),
            ..Title::default()
        },
    }
}

#[test]
fn two_series_interleaved_keep_their_order_by_their_newest_episode() {
    let slots = shown(vec![
        episode(8, "2026-09-03", 1),
        other(4, "2026-09-01"),
        movie("movie:1", "2026-08-30"),
        episode(7, "2026-08-27", 1),
        other(3, "2026-08-20"),
    ]);
    assert_eq!(ids(&slots), ["series:1", "series:2", "movie:1"]);
    assert_eq!(slots[0].released, "2026-09-03");
    assert_eq!(slots[1].released, "2026-09-01");
    assert_eq!(slots[1].title, "Part 4");
    assert_eq!(slots[1].new, 2);
    assert_eq!(slots[2].kind, "movies");
}

#[test]
fn an_episode_with_no_full_date_folds_under_airing() {
    let slots = fold(vec![episode(1, "2004", 0)], Fold::Airing);
    assert_eq!(ids(&slots), ["series:1"]);
    assert_eq!(slots[0].released, "2004");
}

#[test]
fn a_series_appears_once_however_its_episodes_interleave() {
    let slots = fold(
        vec![
            episode(3, "2026-09-03", 400),
            movie("movie:1", "2026-09-01"),
            episode(2, "2026-08-27", 400),
        ],
        Fold::Titles,
    );
    assert_eq!(ids(&slots), ["series:1", "movie:1"]);
}

#[test]
fn a_movie_is_its_own_slot_under_every_fold() {
    for rule in [
        Fold::Titles,
        Fold::Episodes,
        Fold::Airing,
        Fold::Shows { today: 0 },
    ] {
        let slots = fold(vec![movie("movie:1", "1999")], rule);
        assert_eq!(ids(&slots), ["movie:1"]);
        assert_eq!(slots[0].kind, "movies");
    }
}

// One page of one serial's season. Every episode is older than the
// window, so the page folds to one slot.
fn a_season(page: usize) -> Vec<Candidate> {
    (0..CANDIDATES)
        .map(|index| {
            let number = (page * CANDIDATES + index) as i64;
            episode(number, "2004-03-01", 400)
        })
        .collect()
}

fn movies(from: usize, count: usize) -> Vec<Candidate> {
    (from..from + count)
        .map(|number| movie(&format!("movie:{number}"), "2001"))
        .collect()
}

#[test]
fn a_read_uses_four_real_pages_to_fill_after_a_season_drop() {
    let mut candidates = Vec::new();
    for page in 0..4 {
        let mut part = a_season(page);
        part.truncate(CANDIDATES - FILL / 4);
        part.extend(movies(page * (FILL / 4), FILL / 4));
        assert_eq!(part.len(), CANDIDATES);
        candidates.extend(part);
    }

    let slots = filled(Fold::Titles, candidates);
    assert_eq!(slots.len(), FILL + 1);
    assert_eq!(slots.iter().filter(|slot| slot.kind == "series").count(), 1);
}

#[test]
fn a_season_drop_crosses_pages_before_the_read_fills() {
    let mut first = a_season(0);
    first.truncate(CANDIDATES - 10);
    first.extend(movies(0, 10));

    let mut second = a_season(1);
    second.truncate(CANDIDATES - 37);
    second.extend(movies(10, 37));

    let candidates: Vec<Candidate> = first.into_iter().chain(second).collect();
    let expected = fold(candidates.clone(), Fold::Titles);
    let actual = filled(Fold::Titles, candidates);

    assert_eq!(actual.len(), FILL);
    assert_eq!(actual, expected);
}

#[test]
fn a_first_page_with_a_fill_stops_at_that_candidate_prefix() {
    let candidates = movies(0, PAGES * CANDIDATES);
    let expected = fold(candidates[..CANDIDATES].to_vec(), Fold::Titles);
    let actual = filled(Fold::Titles, candidates);

    assert!(actual.len() >= FILL);
    assert_eq!(actual, expected);
}

#[test]
fn a_short_first_page_ends_the_read() {
    let slots = filled(Fold::Titles, vec![movie("movie:1", "2001")]);
    assert_eq!(ids(&slots), ["movie:1"]);
}

#[test]
fn a_short_last_page_ends_a_read_after_a_full_page() {
    let mut candidates = a_season(0);
    candidates.extend(movies(0, 3));
    let expected = fold(candidates.clone(), Fold::Titles);
    assert_eq!(filled(Fold::Titles, candidates), expected);
}

#[test]
fn the_page_bound_ends_a_read_that_never_fills() {
    let candidates: Vec<Candidate> = (0..PAGES + 1).flat_map(a_season).collect();
    assert_eq!(ids(&filled(Fold::Titles, candidates)), ["series:1"]);
}

#[test]
fn a_candidate_after_the_page_bound_does_not_enter_the_fold() {
    let mut candidates: Vec<Candidate> = (0..PAGES).flat_map(a_season).collect();
    candidates.push(movie("movie:beyond", "2001"));
    let expected = fold(candidates[..PAGES * CANDIDATES].to_vec(), Fold::Titles);
    assert_eq!(filled(Fold::Titles, candidates), expected);
}

#[test]
fn a_show_beyond_a_full_first_page_does_not_change_its_count_or_still() {
    let rule = Fold::Shows { today: today() };
    let mut candidates = vec![episode(1, "2026-03-01", 400)];
    candidates.extend(movies(0, CANDIDATES - 1));
    candidates.push(episode(2, "2026-09-03", -400));
    candidates.extend(movies(CANDIDATES - 1, PAGES * CANDIDATES - CANDIDATES - 1));
    let expected = fold(candidates[..CANDIDATES].to_vec(), rule);
    let with_later_episode = fold(candidates.clone(), rule);
    let actual = filled(rule, candidates);

    assert_eq!(actual, expected);
    assert_eq!(actual[0].new, 0);
    assert_eq!(actual[0].art, "still-1.jpg");
    assert_eq!(with_later_episode[0].new, 1);
    assert_eq!(with_later_episode[0].art, "still-2.jpg");
}

#[test]
fn the_strip_shows_fewer_than_the_read_takes() {
    const { assert!(SHOWN < CANDIDATES) };
    const { assert!(WINDOW_DAYS > 0) };
}
