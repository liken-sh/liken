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

// The row that holds focus, and the slot inside that strip.
fn at(browser: &Browser<Fake, NoPosters>) -> (usize, usize) {
    let home = showing_home(browser);
    let strip = home.blocks[home.focus]
        .strip()
        .expect("a strip holds focus");
    (home.focus, strip.focus)
}

// The strips of the page in order, the banner left out.
fn strips(browser: &Browser<Fake, NoPosters>) -> Vec<&crate::screens::home::Strip> {
    showing_home(browser)
        .blocks
        .iter()
        .filter_map(|block| block.strip())
        .collect()
}

// A browser with recent titles and focus on the first strip. The page
// opens on the banner, and these tests are about the strips.
fn on_strips(movies: usize) -> Browser<Fake, NoPosters> {
    let mut browser = with_recent(movies);
    browser.key("down");
    browser
}

#[test]
fn the_page_holds_the_two_recency_strips_over_the_libraries() {
    let browser = on_strips(3);
    let home = showing_home(&browser);
    assert_eq!(
        headings(&browser),
        ["Recently released", "Recently added", "Libraries"]
    );
    assert_eq!(home.focus, 1);
    assert_eq!(home.control, None);

    let strips = strips(&browser);
    let released = strips[0];
    assert!(released.see_all);
    assert_eq!(released.lines, 2);
    assert_eq!(released.items.len(), 2);
    assert_eq!(released.items[0].caption, "S01E02 · The Serial");
    assert_eq!(released.items[0].art, "s1e2.jpg");
    assert_eq!(released.items[1].caption, "The Serial");
    let added = strips[1];
    assert_eq!(added.items.len(), 1);
    assert_eq!(added.items[0].caption, "Entry 1");
    assert!(!strips[2].see_all);
    assert_eq!(strips[2].lines, 2);
}

#[test]
fn up_and_down_move_between_the_strips_and_each_remembers_its_focus() {
    let mut browser = on_strips(3);
    browser.key("right");
    assert_eq!(at(&browser), (1, 1));

    browser.key("down");
    assert_eq!(at(&browser), (2, 0));
    browser.key("right");
    browser.key("down");
    assert_eq!(at(&browser), (3, 0));
    browser.key("down");
    assert_eq!(at(&browser), (3, 0));

    browser.key("up");
    assert_eq!(at(&browser), (2, 1));
    browser.key("up");
    assert_eq!(at(&browser), (1, 1));
}

#[test]
fn right_reaches_see_all_and_no_further() {
    let mut browser = on_strips(3);
    for _ in 0..6 {
        browser.key("right");
    }
    assert_eq!(at(&browser), (1, 2));
    browser.key("left");
    assert_eq!(at(&browser), (1, 1));
}

#[test]
fn up_from_the_first_strip_reaches_the_banner_then_the_band_and_down_returns() {
    let mut browser = on_strips(3);
    browser.key("right");

    browser.key("up");
    assert_eq!(showing_home(&browser).focus, 0);
    browser.key("up");

    assert_eq!(showing_home(&browser).control, Some(0));
    browser.key("right");
    assert_eq!(showing_home(&browser).control, Some(1));
    browser.key("enter");
    assert!(browser.stack.is_empty());
    assert_eq!(showing_home(&browser).control, Some(1));

    browser.key("down");

    assert_eq!(showing_home(&browser).control, None);
    assert_eq!(showing_home(&browser).focus, 0);
    browser.key("down");
    assert_eq!(at(&browser), (1, 1));
}

#[test]
fn an_empty_strip_is_skipped_and_up_from_the_libraries_reaches_the_band() {
    let mut browser = browser(3);
    assert_eq!(at(&browser), (3, 0));

    browser.key("up");

    assert_eq!(showing_home(&browser).control, Some(0));
    browser.key("down");
    assert_eq!(at(&browser), (3, 0));
}

#[test]
fn a_select_on_an_episode_opens_the_series_page_on_that_episode() {
    let mut browser = on_strips(3);

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.library, SERIALS);
    assert_eq!(page.focus, SeriesFocus::Still(1));
    assert_eq!(page.stills[1].caption, "E2 · Segment 2");
}

#[test]
fn a_select_on_a_folded_series_opens_its_page_on_the_first_episode() {
    let mut browser = on_strips(3);
    browser.key("right");

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(0));
}

#[test]
fn a_select_on_a_movie_opens_its_page() {
    let mut browser = on_strips(3);
    browser.key("down");

    browser.key("enter");

    assert_eq!(showing_page(&browser).id, "movies:1");
}

#[test]
fn see_all_opens_the_wall_of_every_title_and_back_returns_to_it() {
    let mut browser = on_strips(3);
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

    assert_eq!(at(&browser), (2, 1));
}

#[test]
fn the_released_strip_holds_the_window_of_today_and_the_added_strip_the_rest() {
    let browser = on_strips(3);
    let strips = strips(&browser);
    let released: Vec<&str> = strips[0]
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();
    let added: Vec<&str> = strips[1]
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();
    assert_eq!(released, ["episode:1:2", SERIAL]);
    assert_eq!(added, ["movies:1"]);
    assert!(released.iter().all(|id| !added.contains(id)));
}

#[test]
fn the_walls_behind_see_all_are_neither_windowed_nor_subtracted() {
    let mut browser = on_strips(3);
    browser.key("right");
    browser.key("right");
    browser.key("enter");
    let wall = showing_wall(&browser);
    assert_eq!(wall.slots.query, Query::Released { fold: Fold::Titles });
    let ids: Vec<&str> = wall
        .slots
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();
    assert_eq!(ids, [SERIAL, "movies:1"]);
    assert_eq!(wall.slots.items[1].name, "Entry 1");

    browser.key("escape");
    browser.key("down");
    browser.key("right");
    browser.key("enter");
    let wall = showing_wall(&browser);
    assert_eq!(wall.slots.query, Query::Added { fold: Fold::Titles });
    let ids: Vec<&str> = wall
        .slots
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();
    assert_eq!(ids, [SERIAL, "movies:1"]);
}

#[test]
fn a_select_on_a_library_opens_its_wall() {
    let mut browser = on_strips(3);
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
    assert_eq!(at(&browser), (3, 1));

    browser.source.recent = true;
    browser.source.changed = true;
    assert!(browser.pump(1.0));

    assert_eq!(at(&browser), (3, 1));
    assert_eq!(strips(&browser)[0].items.len(), 2);
}

#[test]
fn a_change_that_empties_the_focused_strip_moves_focus_to_the_next() {
    let mut browser = on_strips(3);
    browser.key("right");

    browser.source.recent = false;
    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(at(&browser), (3, 0));
}

#[test]
fn a_change_that_empties_every_strip_leaves_focus_in_the_band() {
    let mut browser = with_recent(3);
    browser.source.recent = false;
    browser.source.movies = 0;
    browser.source.changed = true;
    browser.pump(1.0);
    assert_eq!(strips(&browser)[2].items.len(), 2);

    browser.key("down");
    browser.key("down");
    assert_eq!(at(&browser), (3, 0));
}

#[test]
fn a_rest_on_a_title_asks_for_its_backdrop_and_on_a_library_for_nothing() {
    let mut browser = on_strips(3);
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
    let mut browser = on_strips(3);
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
    let mut browser = on_strips(3);
    browser.tick(0.0);
    for _ in 0..3 {
        browser.key("right");
    }
    assert!(browser.next_frame(0.0).is_none());
    browser.key("up");
    browser.key("up");
    assert!(browser.next_frame(0.0).is_none());
}

#[test]
fn the_view_builds_with_strips_and_with_the_band_in_focus() {
    let mut browser = on_strips(3);
    let _ = browser.view();
    browser.key("down");
    browser.key("down");
    let _ = browser.view();
    for _ in 0..4 {
        browser.key("up");
    }
    assert_eq!(showing_home(&browser).control, Some(0));
    let _ = browser.view();
    let _ = super::browser(0).view();
}

// A browser whose pool holds every kind, so the page draws four strips
// between the recency strips and the libraries.
fn with_draw() -> Browser<Fake, NoPosters> {
    Browser::new(
        Fake {
            movies: 3,
            recent: true,
            people: true,
            sets: true,
            pool: true,
            ..Fake::default()
        },
        NoPosters::default(),
    )
}

fn headings(browser: &Browser<Fake, NoPosters>) -> Vec<&str> {
    strips(browser)
        .into_iter()
        .map(|strip| strip.heading.as_str())
        .collect()
}

#[test]
fn the_drawn_strips_sit_between_the_recency_strips_and_the_libraries() {
    let browser = with_draw();
    let headings = headings(&browser);
    assert_eq!(headings.len(), 7);
    assert_eq!(headings[0], "Recently released");
    assert_eq!(headings[1], "Recently added");
    assert_eq!(headings[6], "Libraries");
    let mut drawn: Vec<&str> = headings[2..6].to_vec();
    drawn.sort_unstable();
    assert_eq!(drawn, ["A Player", "Drama", "The Entries", "Western"]);
    let first_three = &headings[2..5];
    assert!(first_three.contains(&"A Player"));
    assert!(first_three.contains(&"The Entries"));
    assert!(strips(&browser)[2..6].iter().all(|strip| strip.see_all));
}

#[test]
fn a_drawn_strip_captions_a_title_with_its_facts_and_a_persons_with_the_parts() {
    let browser = with_draw();
    let strips = strips(&browser);
    let western = strips
        .iter()
        .find(|strip| strip.heading == "Western")
        .expect("the page drew Western");
    assert_eq!(western.items[0].caption, "Entry 1");
    assert_eq!(western.items[0].under, "1980 · 1h 30m · PG");
    let player = strips
        .iter()
        .find(|strip| strip.heading == PLAYER)
        .expect("the page drew the player");
    assert_eq!(player.items[0].caption, "Entry 1 · 1980");
    assert_eq!(player.items[0].under, "Director");
    let set = strips
        .iter()
        .find(|strip| strip.heading == "The Entries")
        .expect("the page drew the set");
    assert_eq!(set.items.len(), 3);
    assert_eq!(set.items[0].under, "1980 · 1h 30m · PG");
}

#[test]
fn see_all_on_a_drawn_strip_opens_its_query_as_a_wall() {
    let mut browser = with_draw();
    let index = headings(&browser)
        .iter()
        .position(|heading| *heading == "Western")
        .expect("the page drew Western");
    for _ in 0..=index {
        browser.key("down");
    }
    for _ in 0..4 {
        browser.key("right");
    }

    browser.key("enter");

    let wall = showing_wall(&browser);
    assert_eq!(wall.heading, "Western · 3");
    assert_eq!(
        wall.slots.query,
        Query::Genre {
            name: "Western".into(),
            order: Order::Released,
        }
    );
}

#[test]
fn a_reread_keeps_focus_on_the_drawn_strip_it_was_on() {
    let mut browser = with_draw();
    for _ in 0..4 {
        browser.key("down");
    }
    browser.key("right");
    let before = headings(&browser)[3].to_string();

    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(at(&browser), (4, 1));
    assert_eq!(headings(&browser)[3], before);
}

#[test]
fn a_pool_that_empties_takes_its_strips_with_it() {
    let mut browser = with_draw();
    for _ in 0..4 {
        browser.key("down");
    }

    browser.source.pool = false;
    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(
        headings(&browser),
        ["Recently released", "Recently added", "Libraries"]
    );
    assert_eq!(at(&browser), (3, 0));
}

#[test]
fn a_wake_and_a_present_read_the_home_page_and_its_pool_again() {
    let (mut browser, bus) = on_bus(3, vec![Moment::Sleep]);
    browser.pump(1.0);
    browser.source.calls.clear();
    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Wake];

    browser.pump(2.0);

    assert!(browser.source.calls.contains(&"pool"));
    assert!(browser.source.calls.contains(&"libraries"));

    browser.source.calls.clear();
    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Present];
    browser.pump(3.0);
    assert!(browser.source.calls.contains(&"pool"));
}
