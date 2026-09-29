// Arrival from a home card that names one episode: the series page opens
// on that episode's row, with focus on its first button, and back from
// that row returns to the home page in one press.

use super::resume::{plays, reached, watched};
use super::*;

// The browser on the home page with the continue-watching row read, and
// focus on its card at this place. The first card is an episode of
// another serial that the audience has not started, and the second is the
// episode of the fake serial they stopped inside.
fn on_card(place: usize) -> Browser<Fake, NoArt> {
    let (mut browser, _) = watched(plays(), false);
    browser.source.episodes_reached = vec![reached(1_200, 2_760, (1, 2), 30)];
    browser.key("up");
    for _ in 0..place {
        browser.key("right");
    }
    browser
}

fn first_button(browser: &Browser<Fake, NoArt>) -> &'static str {
    showing_series(browser).buttons()[0].word()
}

#[test]
fn a_card_they_stopped_inside_arrives_on_resume() {
    let mut browser = on_card(1);

    browser.key("enter");

    assert_eq!(showing_series(&browser).focus, SeriesFocus::Episode(1, 0));
    assert_eq!(first_button(&browser), "Resume");
}

#[test]
fn a_card_they_have_not_started_arrives_on_play() {
    let mut browser = on_card(0);

    browser.key("enter");

    assert_eq!(showing_series(&browser).focus, SeriesFocus::Episode(2, 0));
    assert_eq!(first_button(&browser), "Play");
}

#[test]
fn back_from_the_row_of_an_arrival_returns_to_the_home_page() {
    let mut browser = on_card(1);
    browser.key("enter");

    browser.key("escape");

    showing_home(&browser);
}

#[test]
fn back_from_a_row_opened_again_from_the_wall_closes_the_row() {
    let mut browser = on_card(1);
    browser.key("enter");
    browser.key("down");
    browser.key("down");
    browser.key("enter");

    browser.key("escape");

    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(1));
}
