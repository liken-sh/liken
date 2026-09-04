// The rest on a wall: what the browser asks the poster store for once
// focus stands still, and when it asks for nothing.

use super::*;

#[test]
fn a_press_on_a_wall_schedules_the_rest() {
    let browser = resting(3);
    assert_eq!(browser.next_frame(0.0), Some(REST));
}

#[test]
fn a_rest_on_a_wall_asks_for_the_backdrop_at_page_size() {
    let mut browser = resting(3);

    browser.tick(REST);

    assert!(browser.next_frame(REST).is_none());
    assert!(browser.posters.get_mut().asked.contains(&(
        "screening/films".into(),
        "movies:1.backdrop.jpg".into(),
        1920,
        1080
    )));
}

#[test]
fn a_press_before_the_rest_moves_the_ask_to_the_item_it_lands_on() {
    let mut browser = resting(3);
    browser.tick(0.1);
    browser.key("right");

    browser.tick(REST);
    let early: Vec<&(String, String, u32, u32)> = browser
        .posters
        .get_mut()
        .asked
        .iter()
        .filter(|(_, _, width, _)| *width == 1920)
        .collect();
    assert!(early.is_empty());

    browser.tick(0.1 + REST);
    let asked = &browser.posters.get_mut().asked;
    assert!(asked.contains(&(
        "screening/films".into(),
        "movies:2.backdrop.jpg".into(),
        1920,
        1080
    )));
}

#[test]
fn a_page_schedules_no_rest() {
    let mut browser = resting(3);
    browser.key("enter");
    assert!(browser.next_frame(1.0).is_none());
}

#[test]
fn the_band_schedules_no_rest() {
    let mut browser = resting(3);
    browser.key("up");
    assert!(browser.next_frame(1.0).is_none());
}

#[test]
fn a_series_wall_rests_on_the_backdrop_of_the_page_it_opens() {
    let mut browser = browser(3);
    browser.tick(0.0);
    browser.key("right");
    browser.key("enter");
    assert_eq!(browser.next_frame(0.0), Some(REST));

    browser.tick(REST);

    assert!(browser.posters.get_mut().asked.contains(&(
        "screening/serials".into(),
        "series:1.backdrop.jpg".into(),
        1920,
        1080
    )));
}

#[test]
fn a_window_of_another_size_asks_for_a_backdrop_of_that_size() {
    let mut browser = resting(3).with_page((1280, 720));
    browser.tick(REST);
    assert!(browser.posters.get_mut().asked.contains(&(
        "screening/films".into(),
        "movies:1.backdrop.jpg".into(),
        1280,
        720
    )));
}

#[test]
fn a_movie_with_no_backdrop_asks_for_nothing() {
    let mut browser = browser(3);
    browser.tick(0.0);
    browser.key("enter");
    browser.source.movies = 0;

    browser.tick(REST);

    assert!(browser.posters.get_mut().asked.is_empty());
}
