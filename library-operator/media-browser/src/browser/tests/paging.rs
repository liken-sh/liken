// The browser's navigation: which screen a press opens, where back
// climbs to, and what a change re-reads.

use super::*;

#[test]
fn the_first_screen_is_the_home_page_and_its_libraries_strip() {
    let browser = browser(3);
    let home = showing_home(&browser);
    let libraries = strip_at(&browser, 3);
    assert_eq!(libraries.heading, "Libraries");
    assert_eq!(libraries.items[0].name, "films");
    assert_eq!(libraries.items[0].under, "movies · 3");
    assert_eq!(libraries.items[0].art, "1.jpg");
    assert_eq!(libraries.items[1].name, "serials");
    assert_eq!(libraries.items[1].under, "series · 2");
    assert_eq!(home.focus, 3);
    assert!(strip_at(&browser, 1).items.is_empty());
}

#[test]
fn enter_opens_a_movies_wall() {
    let mut browser = browser(3);
    browser.key("enter");
    let wall = showing_wall(&browser);
    assert_eq!(wall.slots.items.len(), 3);
    assert_eq!(wall.heading, "films · 3");
}

#[test]
fn a_select_on_a_movie_opens_its_page() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 2);
    assert_eq!(showing_page(&browser).id, "movies:1");
}

#[test]
fn a_select_on_a_series_opens_its_page() {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(browser.stack.len(), 2);
    assert_eq!(page.id, "series:1");
    assert_eq!(page.facts, "1980 · 2 seasons · TV-14");
    assert_eq!(page.seasons.len(), 2);
    assert_eq!(page.stills.len(), 8);
    assert_eq!(page.stills[0].caption, "E1 · Segment 1");
}

#[test]
fn a_press_on_a_series_page_crosses_its_dividers() {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    browser.key("down");

    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(4));
    browser.key("up");
    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(0));
}

#[test]
fn a_walk_from_a_title_to_a_person_and_back_climbs_the_stack_it_built() {
    let mut browser = browser(3);
    browser.source.people = true;

    for _ in 0..2 {
        browser.key("enter");
        assert_eq!(showing_wall(&browser).slots.items.len(), 3);

        browser.key("enter");
        assert_eq!(showing_page(&browser).id, "movies:1");

        browser.key("down");
        assert_eq!(showing_page(&browser).focus, Focus::Stripe(0, 0));

        browser.key("enter");
        assert_eq!(showing_person(&browser).name, "A Player");
        assert_eq!(showing_person(&browser).works.items.len(), 3);

        browser.key("right");
        browser.key("enter");
        assert_eq!(showing_page(&browser).id, "movies:2");
        assert_eq!(browser.stack.len(), 4);

        browser.key("escape");
        assert_eq!(showing_person(&browser).works.focus, 1);
        browser.key("escape");
        assert_eq!(showing_page(&browser).id, "movies:1");
        browser.key("escape");
        assert_eq!(showing_wall(&browser).slots.items.len(), 3);
        browser.key("escape");
        assert!(browser.stack.is_empty());
    }
}

#[test]
fn back_climbs_one_screen_and_stops_at_the_home_page() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("escape");
    assert!(browser.stack.is_empty());
    browser.key("escape");
    assert!(browser.stack.is_empty());
    assert!(matches!(browser.top(), screens::Screen::Home(_)));
}

#[test]
fn backspace_goes_back_too() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("backspace");
    assert!(browser.stack.is_empty());
}

#[test]
fn back_rereads_the_screen_it_uncovers() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("enter");
    browser.source.calls.clear();

    browser.key("escape");

    assert!(browser.source.calls.contains(&"wall"));
}

#[test]
fn back_from_a_page_returns_to_the_wall_at_the_same_focus() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("right");
    browser.key("right");

    browser.key("enter");
    browser.key("escape");

    assert_eq!(showing_wall(&browser).slots.focus, 2);
}

#[test]
fn arrows_move_focus_on_a_strip_and_a_wall() {
    let mut browser = browser(20);
    browser.key("right");
    assert_eq!(strip_at(&browser, 3).focus, 1);
    browser.key("left");
    assert_eq!(strip_at(&browser, 3).focus, 0);
    browser.key("enter");
    browser.key("right");
    assert_eq!(showing_wall(&browser).slots.focus, 1);
    browser.key("down");
    assert_eq!(showing_wall(&browser).slots.focus, 1 + wall::COLUMNS);
}

#[test]
fn an_empty_wall_selects_nothing() {
    let mut browser = browser(0);
    browser.key("enter");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn up_from_the_first_row_reaches_the_band_and_down_gives_the_focus_back() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("right");

    browser.key("up");

    assert_eq!(showing_wall(&browser).control, Some(0));
    browser.key("right");
    assert_eq!(showing_wall(&browser).control, Some(1));
    browser.key("left");
    browser.key("left");
    assert_eq!(showing_wall(&browser).control, Some(0));

    browser.key("down");

    assert_eq!(showing_wall(&browser).control, None);
    assert_eq!(showing_wall(&browser).slots.focus, 1);
}

#[test]
fn the_band_reaches_no_further_than_its_three_controls() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("up");
    for _ in 0..band::CONTROLS.len() + 2 {
        browser.key("right");
    }
    assert_eq!(
        showing_wall(&browser).control,
        Some(band::CONTROLS.len() - 1)
    );
}

#[test]
fn a_select_in_the_band_opens_nothing() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("up");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 1);
    assert_eq!(showing_wall(&browser).control, Some(0));
}

#[test]
fn up_from_a_later_row_stays_on_the_wall() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("down");

    browser.key("up");

    assert_eq!(showing_wall(&browser).control, None);
    assert_eq!(showing_wall(&browser).slots.focus, 0);
}

#[test]
fn the_focused_line_carries_the_facts_the_row_holds() {
    let mut browser = browser(3);
    browser.key("enter");
    assert_eq!(
        showing_wall(&browser).slots.items[0].line.words(),
        "Entry 1 · 1980"
    );
}

#[test]
fn pump_without_a_change_folds_nothing() {
    let mut browser = browser(3);
    let reads = browser.source.calls.len();
    assert!(!browser.pump(1.0));
    assert_eq!(browser.source.calls.len(), reads);
}

#[test]
fn pump_rereads_what_is_shown() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.source.movies = 2;
    browser.source.changed = true;
    assert!(browser.pump(1.0));
    assert_eq!(showing_wall(&browser).slots.items.len(), 2);
    assert_eq!(showing_wall(&browser).heading, "films · 2");
    let libraries = browser
        .source
        .calls
        .iter()
        .filter(|call| **call == "libraries");
    assert_eq!(libraries.count(), 1);
}

#[test]
fn a_delivered_poster_draws_a_frame_and_reads_no_rows() {
    let mut browser = browser(3);
    browser.key("enter");
    let reads = browser.source.calls.len();
    browser.posters.get_mut().delivers = true;
    assert!(browser.pump(1.0));
    assert_eq!(browser.source.calls.len(), reads);
    assert!(!browser.pump(2.0));
}

#[test]
fn a_reread_that_shrinks_clamps_the_focus() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("right");
    browser.key("right");
    browser.source.movies = 1;
    browser.source.changed = true;
    assert!(browser.pump(1.0));
    assert_eq!(showing_wall(&browser).slots.focus, 0);
}

#[test]
fn the_wake_reaches_the_source() {
    let mut browser = browser(3);
    Screen::wake_by(&mut browser, Arc::new(|| {}));
    assert!(browser.source.woken);
}

#[test]
fn a_still_browser_schedules_the_clocks_frame_alone() {
    let mut browser = browser(3);
    browser.tick(4.2);
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(4.2), Some(MINUTE));
}

#[test]
fn the_browser_draws_on_the_theme_ground() {
    assert_eq!(browser(3).background(), look::BACKGROUND);
}

#[test]
fn the_view_builds_for_every_screen() {
    let mut films = browser(3);
    films.source.sets = true;
    films.source.trailers = true;
    let _ = films.view();
    films.key("enter");
    let _ = films.view();
    films.key("up");
    let _ = films.view();
    films.key("down");
    films.key("enter");
    let _ = films.view();
    films.key("down");
    let _ = films.view();

    let mut serials = browser(3);
    serials.key("right");
    serials.key("enter");
    let _ = serials.view();
    serials.key("enter");
    let _ = serials.view();
    serials.key("down");
    let _ = serials.view();
}

#[test]
fn a_change_rereads_the_series_page_that_is_shown() {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    browser.source.changed = true;
    assert!(browser.pump(1.0));
    assert_eq!(browser.source.calls.last(), Some(&"credits"));
    assert_eq!(showing_series(&browser).stills.len(), 8);
}

#[test]
fn a_series_with_no_episodes_plays_nothing() {
    let mut browser = browser(3);
    browser.source.empty = true;
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 2);
    assert!(showing_series(&browser).stills.is_empty());

    browser.key("enter");

    assert_eq!(browser.source.chosen, None);
}

#[test]
fn a_select_on_a_still_asks_for_the_episode_and_its_season() {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    browser.key("right");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/serials".into(),
            Selection::Episode {
                series: "series:1".into(),
                season: 1,
                episode: 2,
            }
        ))
    );
}

#[test]
fn a_reread_of_a_shorter_set_clamps_the_strips_focus() {
    let mut browser = browser(3);
    browser.source.sets = true;
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("right");
    browser.key("right");
    assert_eq!(showing_page(&browser).focus, Focus::Strip(2));

    browser.source.movies = 2;
    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(showing_page(&browser).focus, Focus::Strip(1));
}
