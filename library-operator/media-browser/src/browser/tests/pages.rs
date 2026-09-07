// The two pages a title plays from, over a progress store: the button row
// a film the audience is in the middle of draws, the episode a show's wall
// opens on, and the second the request a press publishes starts at.

use super::*;
use crate::catalog::Progress;
use crate::views::Card;

// The one `Person` these cases record their plays against.
const WATCHER: &str = "first";

// How long every episode of the fake serial is.
const RUNTIME: i64 = 2_760;

// One play of one work: how far it reached, how long the work is, and the
// aired numbers a play of an episode carries.
fn reached(position: i64, duration: i64, numbers: (i64, i64)) -> Progress {
    Progress {
        play: format!("play-{position}"),
        position,
        duration,
        finished: crate::catalog::progress::finished(position, duration),
        recorded: 10,
        season: numbers.0,
        episode: numbers.1,
        ..Progress::default()
    }
}

// A browser on a bus, over an audience of one, whose progress store holds
// these plays of the first film and these plays of the serial.
fn watching(film: Option<Progress>, episodes: Vec<Progress>) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    if let Some(film) = film {
        browser.source.reached.insert(
            ("screening/films".to_string(), "movies:1".to_string()),
            film,
        );
    }
    browser.source.episodes_reached = episodes;
    let browser = browser.with_audience(
        vec![crate::audience::Person {
            name: WATCHER.into(),
            display_name: "First".into(),
        }],
        vec![WATCHER.into()],
    );
    (browser, bus)
}

// The browser on the first film's page, two presses down from the
// libraries strip.
fn on_the_film(browser: &mut Browser<Fake, NoArt>) {
    browser.key("enter");
    browser.key("enter");
}

// The browser on the serial's page, which is the second library of the
// strip.
fn on_the_serial(browser: &mut Browser<Fake, NoArt>) {
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
}

// The words of the film page's button row.
fn words(browser: &Browser<Fake, NoArt>) -> Vec<&'static str> {
    showing_page(browser)
        .buttons()
        .iter()
        .map(|button| button.word())
        .collect()
}

#[test]
fn a_film_the_audience_is_in_the_middle_of_opens_on_resume() {
    let (mut browser, _bus) = watching(Some(reached(600, 5_400, (0, 0))), Vec::new());

    on_the_film(&mut browser);

    assert_eq!(words(&browser), ["Resume", "Start over"]);
    assert_eq!(showing_page(&browser).focus, Focus::Buttons(0));
    assert_eq!(browser.source.watching, [WATCHER.to_string()]);
}

#[test]
fn a_film_the_audience_finished_opens_on_play() {
    let (mut browser, _bus) = watching(Some(reached(5_400, 5_400, (0, 0))), Vec::new());

    on_the_film(&mut browser);

    assert_eq!(words(&browser), ["Play"]);
}

#[test]
fn a_film_no_play_names_opens_on_play() {
    let (mut browser, _bus) = watching(None, Vec::new());

    on_the_film(&mut browser);

    assert_eq!(words(&browser), ["Play"]);
    assert_eq!(showing_page(&browser).progress, None);
}

#[test]
fn a_press_on_resume_publishes_the_second_the_audience_reached() {
    let (mut browser, bus) = watching(Some(reached(600, 5_400, (0, 0))), Vec::new());
    on_the_film(&mut browser);

    browser.key("enter");

    assert_eq!(published(&bus)["start"], "600");
}

#[test]
fn a_press_on_start_over_publishes_no_second() {
    let (mut browser, bus) = watching(Some(reached(600, 5_400, (0, 0))), Vec::new());
    on_the_film(&mut browser);
    browser.key("right");

    browser.key("enter");

    assert_eq!(published(&bus).get("start"), None);
}

#[test]
fn a_shows_wall_opens_on_the_episode_the_audience_watches_next() {
    let (mut browser, _bus) = watching(
        None,
        vec![
            reached(RUNTIME, RUNTIME, (1, 1)),
            reached(900, RUNTIME, (1, 2)),
        ],
    );

    on_the_serial(&mut browser);

    let page = showing_series(&browser);
    assert_eq!(page.focus, SeriesFocus::Still(1));
    let bars: Vec<Option<f32>> = page.stills.iter().map(Card::watched).collect();
    assert_eq!(bars[..3], [Some(1.0), Some(900.0 / RUNTIME as f32), None]);
}

#[test]
fn a_press_on_an_episode_they_are_in_the_middle_of_publishes_the_second() {
    let (mut browser, bus) = watching(None, vec![reached(900, RUNTIME, (1, 2))]);

    on_the_serial(&mut browser);
    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            SERIALS.to_string(),
            Selection::Episode {
                series: SERIAL.into(),
                season: 1,
                episode: 2,
            }
        ))
    );
    assert_eq!(published(&bus)["start"], "900");
}
