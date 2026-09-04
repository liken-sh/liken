// The home page at the browser: its strips, where the arrows take
// focus, what a select opens, and what a rest asks the store for.

use super::*;

// A browser whose recency strips hold an airing episode, a folded
// serial, and a movie.
fn with_recent(movies: usize) -> Browser<Fake, NoPosters> {
    Browser::new(
        Fake {
            movies,
            recent: true,
            ..Fake::default()
        },
        NoPosters::default(),
    )
}

// The strip that holds focus, and the slot inside it.
fn at(browser: &Browser<Fake, NoPosters>) -> (usize, usize) {
    let home = showing_home(browser);
    (home.focus, home.strips[home.focus].focus)
}

#[test]
fn the_page_holds_the_two_recency_strips_over_the_libraries() {
    let browser = with_recent(3);
    let home = showing_home(&browser);
    let headings: Vec<&str> = home
        .strips
        .iter()
        .map(|strip| strip.heading.as_str())
        .collect();
    assert_eq!(
        headings,
        ["Recently released", "Recently added", "Libraries"]
    );
    assert_eq!(home.focus, 0);
    assert_eq!(home.control, None);

    let released = &home.strips[0];
    assert!(released.see_all);
    assert_eq!(released.lines, 1);
    assert_eq!(released.items.len(), 3);
    assert_eq!(released.items[0].caption, "S01E02 · The Serial");
    assert_eq!(released.items[0].art, "s1e2.jpg");
    assert_eq!(released.items[1].caption, "The Serial");
    assert_eq!(released.items[2].caption, "Entry 1");
    assert!(!home.strips[2].see_all);
    assert_eq!(home.strips[2].lines, 2);
}

#[test]
fn up_and_down_move_between_the_strips_and_each_remembers_its_focus() {
    let mut browser = with_recent(3);
    browser.key("right");
    browser.key("right");
    assert_eq!(at(&browser), (0, 2));

    browser.key("down");
    assert_eq!(at(&browser), (1, 0));
    browser.key("right");
    browser.key("down");
    assert_eq!(at(&browser), (2, 0));
    browser.key("down");
    assert_eq!(at(&browser), (2, 0));

    browser.key("up");
    assert_eq!(at(&browser), (1, 1));
    browser.key("up");
    assert_eq!(at(&browser), (0, 2));
}

#[test]
fn right_reaches_see_all_and_no_further() {
    let mut browser = with_recent(3);
    for _ in 0..6 {
        browser.key("right");
    }
    assert_eq!(at(&browser), (0, 3));
    browser.key("left");
    assert_eq!(at(&browser), (0, 2));
}

#[test]
fn up_from_the_first_strip_reaches_the_band_and_down_returns() {
    let mut browser = with_recent(3);
    browser.key("right");

    browser.key("up");

    assert_eq!(showing_home(&browser).control, Some(0));
    browser.key("right");
    assert_eq!(showing_home(&browser).control, Some(1));
    browser.key("enter");
    assert!(browser.stack.is_empty());
    assert_eq!(showing_home(&browser).control, Some(1));

    browser.key("down");

    assert_eq!(showing_home(&browser).control, None);
    assert_eq!(at(&browser), (0, 1));
}

#[test]
fn an_empty_strip_is_skipped_and_up_from_the_libraries_reaches_the_band() {
    let mut browser = browser(3);
    assert_eq!(at(&browser), (2, 0));

    browser.key("up");

    assert_eq!(showing_home(&browser).control, Some(0));
    browser.key("down");
    assert_eq!(at(&browser), (2, 0));
}

#[test]
fn a_select_on_an_episode_opens_the_series_page_on_that_episode() {
    let mut browser = with_recent(3);

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.library, SERIALS);
    assert_eq!(page.focus, SeriesFocus::Still(1));
    assert_eq!(page.stills[1].caption, "E2 · Segment 2");
}

#[test]
fn a_select_on_a_folded_series_opens_its_page_on_the_first_episode() {
    let mut browser = with_recent(3);
    browser.key("right");

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(0));
}

#[test]
fn a_select_on_a_movie_opens_its_page() {
    let mut browser = with_recent(3);
    browser.key("right");
    browser.key("right");

    browser.key("enter");

    assert_eq!(showing_page(&browser).id, "movies:1");
}

#[test]
fn see_all_opens_the_wall_of_every_title_and_back_returns_to_it() {
    let mut browser = with_recent(3);
    browser.key("down");
    for _ in 0..3 {
        browser.key("right");
    }

    browser.key("enter");

    let wall = showing_wall(&browser);
    assert_eq!(wall.heading, "Recently added · 2");
    assert_eq!(wall.slots.query, Query::Added { fold: Fold::Titles });
    assert!(wall.slots.items.iter().all(|item| item.episode.is_none()));

    browser.key("escape");

    assert_eq!(at(&browser), (1, 3));
}

#[test]
fn a_select_on_a_library_opens_its_wall() {
    let mut browser = with_recent(3);
    browser.key("down");
    browser.key("down");
    browser.key("right");

    browser.key("enter");

    let wall = showing_wall(&browser);
    assert_eq!(wall.heading, "serials · 2");
    assert_eq!(
        wall.slots.query,
        Query::Library {
            library: SERIALS.into()
        }
    );
}

#[test]
fn a_change_rereads_the_strips_and_keeps_the_focus() {
    let mut browser = browser(3);
    browser.key("right");
    assert_eq!(at(&browser), (2, 1));

    browser.source.recent = true;
    browser.source.changed = true;
    assert!(browser.pump(1.0));

    assert_eq!(at(&browser), (2, 1));
    assert_eq!(showing_home(&browser).strips[0].items.len(), 3);
}

#[test]
fn a_change_that_empties_the_focused_strip_moves_focus_to_the_next() {
    let mut browser = with_recent(3);
    browser.key("right");

    browser.source.recent = false;
    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(at(&browser), (2, 0));
}

#[test]
fn a_change_that_empties_every_strip_leaves_focus_in_the_band() {
    let mut browser = with_recent(3);
    browser.source.recent = false;
    browser.source.movies = 0;
    browser.source.changed = true;
    browser.pump(1.0);
    assert_eq!(showing_home(&browser).strips[2].items.len(), 2);

    browser.key("down");
    browser.key("down");
    assert_eq!(at(&browser), (2, 0));
}

#[test]
fn a_rest_on_a_title_asks_for_its_backdrop_and_on_a_library_for_nothing() {
    let mut browser = with_recent(3);
    browser.tick(0.0);
    browser.key("right");
    assert_eq!(browser.next_frame(0.0), Some(REST));
    browser.tick(REST);
    assert!(browser.posters.get_mut().asked.contains(&(
        SERIALS.into(),
        format!("{SERIAL}.backdrop.jpg"),
        1920,
        1080
    )));

    browser.key("down");
    browser.key("down");
    assert!(browser.next_frame(REST).is_none());
}

#[test]
fn a_rest_on_an_episode_asks_for_its_series_backdrop() {
    let mut browser = with_recent(3);
    browser.tick(0.0);
    browser.key("left");
    browser.tick(REST);
    assert!(browser.posters.get_mut().asked.contains(&(
        SERIALS.into(),
        format!("{SERIAL}.backdrop.jpg"),
        1920,
        1080
    )));
}

#[test]
fn a_rest_on_see_all_and_in_the_band_asks_for_nothing() {
    let mut browser = with_recent(3);
    browser.tick(0.0);
    for _ in 0..3 {
        browser.key("right");
    }
    assert!(browser.next_frame(0.0).is_none());
    browser.key("up");
    assert!(browser.next_frame(0.0).is_none());
}

#[test]
fn the_view_builds_with_strips_and_with_the_band_in_focus() {
    let mut browser = with_recent(3);
    let _ = browser.view();
    browser.key("down");
    browser.key("down");
    let _ = browser.view();
    browser.key("up");
    browser.key("up");
    browser.key("up");
    let _ = browser.view();
    let _ = super::browser(0).view();
}
