// The progress store changing under a shown screen. The store's stream
// names each play that changed, whatever wrote it: a `Play`'s position, a
// mark, or an outside play. The browser reads again only the progress of
// what the screen draws, with no press, and reads nothing for a play the
// screen does not draw.

use super::resume::{plays, reached, watched};
use super::*;
use crate::screens::movie::row::Button;
use crate::screens::movie::watch::Mark;

// The film the row and the page of these cases draw, and its library.
const FILMS: &str = "screening/films";
const FILM: &str = "movies:1";

// The change the stream names for one play of one work, with the people
// the play is on.
fn touched(id: &str, people: &[&str]) -> Touched {
    Touched {
        works: vec![(FILMS.to_string(), id.to_string())],
        people: people.iter().map(|name| (*name).to_string()).collect(),
    }
}

// Record a mark on the film in the fake store: the film's newest play
// stands at this position, recorded after every play the fixture holds.
fn record(browser: &mut Browser<Fake, NoArt>, position: i64) {
    let mark = reached(position, 5_400, (0, 0), 60);
    for play in &mut browser.source.continues {
        if play.id == FILM {
            play.progress = mark.clone();
        }
    }
    browser
        .source
        .reached
        .insert((FILMS.to_string(), FILM.to_string()), mark);
}

// The browser on the film's page, opened from its card on the row, with
// the reads of the open cleared.
fn on_the_films_page() -> Browser<Fake, NoArt> {
    let (mut browser, _bus) = watched(plays(), false);
    browser.key("up");
    browser.key("right");
    browser.key("right");
    browser.key("enter");
    assert_eq!(showing_page(&browser).id, FILM);
    browser.source.calls.clear();
    browser
}

// The ids of the cards on the continue-watching row.
fn cards(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    super::resume::row(browser)
        .items
        .iter()
        .map(|item| item.id.clone())
        .collect()
}

// The film's page as it draws a film the audience finished: the Play
// row, a full bar, the watched status, and the clear alone.
fn assert_finished(browser: &Browser<Fake, NoArt>) {
    let page = showing_page(browser);
    assert_eq!(page.buttons(), [Button::Play]);
    assert_eq!(page.marks(), [Mark::Cleared]);
    assert_eq!(page.status().words, "Watched");
    assert_eq!(
        crate::screens::movie::watch::share(page.progress.as_ref()),
        1.0
    );
}

#[test]
fn a_mark_recorded_under_the_films_page_redraws_its_row_bar_and_status_with_no_press() {
    let mut browser = on_the_films_page();
    record(&mut browser, 5_400);
    browser.source.touched = vec![touched(FILM, &["first"])];

    assert!(browser.pump(1.0));

    assert_finished(&browser);
}

#[test]
fn a_position_recorded_under_the_films_page_moves_its_bar_and_status_with_no_press() {
    let mut browser = on_the_films_page();
    record(&mut browser, 2_700);
    browser.source.touched = vec![touched(FILM, &["first"])];

    assert!(browser.pump(1.0));

    let page = showing_page(&browser);
    assert_eq!(page.status().words, "0:45:00 watched · 45m remaining");
    assert_eq!(
        crate::screens::movie::watch::share(page.progress.as_ref()),
        0.5
    );
}

#[test]
fn a_clear_recorded_under_the_home_page_takes_the_card_off_the_row_with_no_press() {
    let (mut browser, _bus) = watched(plays(), false);
    assert!(cards(&browser).contains(&FILM.to_string()));
    record(&mut browser, 0);
    browser.source.touched = vec![touched(FILM, &["first"])];

    assert!(browser.pump(1.0));

    assert!(!cards(&browser).contains(&FILM.to_string()));
}

#[test]
fn a_change_under_the_home_page_reads_the_row_and_nothing_else() {
    let (mut browser, _bus) = watched(plays(), false);
    browser.source.calls.clear();
    browser.source.touched = vec![touched(FILM, &["first"])];

    browser.pump(1.0);

    let calls = &browser.source.calls;
    assert!(calls.contains(&"continue_watching"));
    let other_rows: Vec<&&str> = calls
        .iter()
        .filter(|call| ["pool", "libraries", "genres", "wall", "franchises"].contains(call))
        .collect();
    assert_eq!(other_rows, Vec::<&&str>::new());
}

#[test]
fn a_play_that_leaves_out_someone_in_the_room_reads_no_row() {
    let (mut browser, _bus) = watched(plays(), false);
    browser.source.calls.clear();
    browser.source.touched = vec![touched(FILM, &["other"])];

    assert!(!browser.pump(1.0));

    assert_eq!(browser.source.calls, Vec::<&str>::new());
}

#[test]
fn a_change_the_page_does_not_draw_reads_nothing() {
    let cases: &[(&str, &[&str])] = &[("movies:2", &["first"]), (FILM, &["other"])];
    for (id, people) in cases {
        let mut browser = on_the_films_page();
        browser.source.touched = vec![touched(id, people)];

        assert!(!browser.pump(1.0), "{id} for {people:?}");

        assert_eq!(
            browser.source.calls,
            Vec::<&str>::new(),
            "{id} for {people:?}"
        );
    }
}

#[test]
fn a_change_under_a_film_is_read_when_the_film_ends() {
    let (mut browser, bus) = watched(plays(), false);
    browser.key("up");
    browser.key("right");
    browser.key("right");
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(1.0);
    browser.source.calls.clear();
    record(&mut browser, 5_400);
    browser.source.touched = vec![touched(FILM, &["first"])];
    browser.pump(2.0);
    assert_eq!(browser.source.calls, Vec::<&str>::new());

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(3.0);

    assert_finished(&browser);
}

// A film writes its position about once a second. A page that draws the
// film reads the film's progress on each one and never reads the page.
#[test]
fn a_films_positions_read_the_pages_progress_and_never_the_page() {
    let mut browser = on_the_films_page();

    for second in 1..=5 {
        browser.source.touched = vec![touched(FILM, &["first"])];
        browser.pump(f64::from(second));
    }

    let calls = &browser.source.calls;
    assert_eq!(calls.iter().filter(|call| **call == "plays_of").count(), 5);
    assert!(!calls.contains(&"movie"));
}

// A stream that dropped names no play, so the page reads its progress
// again whole.
#[test]
fn a_change_no_play_names_reads_the_pages_progress_again() {
    let mut browser = on_the_films_page();
    record(&mut browser, 5_400);
    browser.source.progressed = true;

    assert!(browser.pump(1.0));

    assert_finished(&browser);
}

// The browser over an audience of one, on the screen these presses from
// the home page open, with no progress in the store yet.
fn opened(presses: &[&str]) -> Browser<Fake, NoArt> {
    let mut browser = browser(3).with_audience(
        vec![crate::audience::Person {
            name: "first".into(),
            display_name: "First".into(),
        }],
        vec!["first".into()],
    );
    browser.pump(0.0);
    for press in presses {
        browser.key(press);
    }
    browser
}

#[test]
fn a_play_of_a_film_on_a_wall_draws_its_bar_with_no_press() {
    let mut browser = opened(&["enter"]);
    browser.source.reached.insert(
        (FILMS.to_string(), "movies:2".to_string()),
        reached(2_700, 5_400, (0, 0), 60),
    );
    browser.source.touched = vec![touched("movies:2", &["first"])];

    assert!(browser.pump(1.0));

    assert_eq!(
        showing_wall(&browser).slots.items[1].progress,
        Some(Played {
            position: 2_700,
            duration: 5_400
        })
    );
}

#[test]
fn a_play_of_an_episode_draws_its_bar_on_the_series_page_with_no_press() {
    let mut browser = opened(&["right", "enter", "enter"]);
    browser.source.episodes_reached = vec![reached(900, 2_760, (1, 1), 60)];
    browser.source.touched = vec![Touched {
        works: vec![(SERIALS.to_string(), SERIAL.to_string())],
        people: vec!["first".into()],
    }];

    assert!(browser.pump(1.0));

    let stills = &showing_series(&browser).stills;
    assert_eq!(
        stills[0].progress.as_ref().map(|reached| reached.position),
        Some(900)
    );
    assert_eq!(stills[0].status().words, "15:00 watched · 31m remaining");
}
