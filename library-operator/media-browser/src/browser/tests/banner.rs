// The banner at the browser: its titles, the arrows, select, and the
// rest.

use super::*;
use crate::screens::home::banner::Banner;
use crate::views::ratings::{Mark, Score};

// A browser whose recency strips hold an episode, a serial, and a
// movie, each with a backdrop.
fn with_banner() -> Browser<Fake, NoPosters> {
    Browser::new(
        Fake {
            movies: 3,
            recent: true,
            ..Fake::default()
        },
        NoPosters::default(),
    )
}

// The banner the home page holds.
fn banner(browser: &Browser<Fake, NoPosters>) -> &Banner {
    showing_home(browser)
        .banner()
        .expect("the home page holds a banner")
}

// The names of the banner's titles in order.
fn names(browser: &Browser<Fake, NoPosters>) -> Vec<&str> {
    banner(browser)
        .titles
        .iter()
        .map(|title| title.name.as_str())
        .collect()
}

#[test]
fn the_page_opens_on_the_banner_with_the_newest_release_and_the_newest_arrival() {
    let browser = with_banner();
    let home = showing_home(&browser);
    assert_eq!(home.focus, 0);
    assert_eq!(home.control, None);
    assert_eq!(names(&browser), ["The Serial", "Entry 1"]);

    let banner = banner(&browser);
    assert_eq!(banner.focus, 0);
    assert_eq!(banner.titles[0].backdrop, format!("{SERIAL}.backdrop.jpg"));
    assert_eq!(banner.titles[0].facts, "1980 · 2 seasons · TV-14");
    assert_eq!(banner.titles[0].item.library, SERIALS);
    assert_eq!(banner.titles[1].backdrop, "movies:1.backdrop.jpg");
    assert_eq!(banner.titles[1].facts, "1980 · 1h 30m · PG");
    assert_eq!(banner.titles[1].item.library, "screening/films");
}

#[test]
fn a_title_carries_its_genres_and_its_scores_apart_from_its_facts() {
    let browser = with_banner();
    let banner = banner(&browser);

    let serial = &banner.titles[0];
    assert_eq!(serial.facts, "1980 · 2 seasons · TV-14");
    assert_eq!(serial.genres, "Adventure, Mystery");
    assert_eq!(
        serial.ratings,
        [Score {
            mark: Mark::Imdb,
            value: 8.1,
        }]
    );

    let movie = &banner.titles[1];
    assert_eq!(movie.facts, "1980 · 1h 30m · PG");
    assert_eq!(movie.genres, "Drama, Western");
    assert_eq!(
        movie.ratings,
        [
            Score {
                mark: Mark::Imdb,
                value: 6.5,
            },
            Score {
                mark: Mark::Tomato,
                value: 83.0,
            }
        ]
    );
}

#[test]
fn the_drawn_strips_feed_the_banner_before_the_recency_strips() {
    let browser = Browser::new(
        Fake {
            movies: 3,
            recent: true,
            people: true,
            sets: true,
            pool: true,
            ..Fake::default()
        },
        NoPosters::default(),
    );
    let names = names(&browser);
    assert_eq!(names.len(), 4);
    assert_eq!(names[3], "The Serial");
    let mut drawn: Vec<&str> = names[..3].to_vec();
    drawn.sort_unstable();
    assert_eq!(drawn, ["Entry 1", "Entry 2", "Entry 3"]);
}

#[test]
fn a_title_with_no_backdrop_never_enters_the_banner_and_focus_skips_it() {
    let mut browser = Browser::new(
        Fake {
            movies: 3,
            recent: true,
            bare: true,
            ..Fake::default()
        },
        NoPosters::default(),
    );
    assert!(banner(&browser).is_empty());
    assert_eq!(showing_home(&browser).focus, 1);

    browser.key("up");
    assert_eq!(showing_home(&browser).control, Some(0));
    browser.key("down");
    assert_eq!(showing_home(&browser).control, None);
    assert_eq!(showing_home(&browser).focus, 1);
}

#[test]
fn left_and_right_move_across_the_banner_and_stop_at_its_ends() {
    let mut browser = with_banner();
    browser.key("right");
    assert_eq!(banner(&browser).focus, 1);
    browser.key("right");
    assert_eq!(banner(&browser).focus, 1);
    browser.key("left");
    assert_eq!(banner(&browser).focus, 0);
    browser.key("left");
    assert_eq!(banner(&browser).focus, 0);
}

#[test]
fn up_from_the_banner_reaches_the_band_and_down_returns_to_the_title_it_held() {
    let mut browser = with_banner();
    browser.key("right");

    browser.key("up");
    assert_eq!(showing_home(&browser).control, Some(0));

    browser.key("down");
    assert_eq!(showing_home(&browser).control, None);
    assert_eq!(showing_home(&browser).focus, 0);
    assert_eq!(banner(&browser).focus, 1);
}

#[test]
fn down_from_the_banner_reaches_the_first_strip_and_up_returns() {
    let mut browser = with_banner();
    browser.key("down");
    assert_eq!(showing_home(&browser).focus, 1);
    browser.key("up");
    assert_eq!(showing_home(&browser).focus, 0);
}

#[test]
fn a_select_on_the_banner_opens_the_titles_page_on_the_episode_it_came_from() {
    let mut browser = with_banner();

    browser.key("enter");
    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(1));

    browser.key("escape");
    browser.key("right");
    browser.key("enter");
    assert_eq!(showing_page(&browser).id, "movies:1");
}

#[test]
fn a_rest_on_the_banner_asks_for_the_titles_page_backdrop() {
    let mut browser = with_banner();
    browser.tick(0.0);
    browser.key("right");
    assert_eq!(browser.next_frame(0.0), Some(REST));

    browser.tick(REST);

    assert!(browser.posters.get_mut().asked.contains(&(
        "screening/films".into(),
        "movies:1.backdrop.jpg".into(),
        1920,
        1080
    )));
}

#[test]
fn a_change_keeps_the_banner_on_the_title_it_held() {
    let mut browser = with_banner();
    browser.key("right");

    browser.source.changed = true;
    browser.pump(1.0);

    assert_eq!(showing_home(&browser).focus, 0);
    assert_eq!(banner(&browser).focus, 1);
    assert_eq!(names(&browser)[1], "Entry 1");
}

#[test]
fn a_change_that_empties_the_banner_moves_focus_to_the_first_strip_that_holds() {
    let mut browser = with_banner();

    browser.source.recent = false;
    browser.source.changed = true;
    browser.pump(1.0);

    assert!(banner(&browser).is_empty());
    assert_eq!(showing_home(&browser).focus, 3);
}

#[test]
fn the_view_builds_with_the_banner_in_focus_and_under_a_scrolled_strip() {
    let mut browser = with_banner();
    let _ = browser.view();
    browser.key("down");
    browser.key("down");
    let _ = browser.view();
}
