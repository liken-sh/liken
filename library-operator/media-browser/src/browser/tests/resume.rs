// The continue-watching row at the browser: what the progress store's
// resumes become on the page, what a finished work leaves behind it, and
// the page a press on one of the slots opens.

use super::*;
use crate::catalog::Progress;

// One work the audience is in the middle of, as the progress read answers
// it.
fn resume(kind: &str, id: &str, title: &str, progress: Progress) -> Resume {
    Resume {
        library: match kind {
            "movie" => "screening/films".to_string(),
            _ => SERIALS.to_string(),
        },
        kind: kind.to_string(),
        id: id.to_string(),
        title: title.to_string(),
        released: "1980".into(),
        art: format!("{id}.jpg"),
        progress,
    }
}

// Where one play reached, by the numbers the store holds.
fn reached(position: i64, duration: i64, numbers: (i64, i64)) -> Progress {
    Progress {
        play: format!("play-{position}"),
        position,
        duration,
        finished: crate::catalog::progress::finished(position, duration),
        running: false,
        recorded: 10,
        season: numbers.0,
        episode: numbers.1,
    }
}

// One resume for every branch the row folds: a film the audience stopped
// in, a film they finished, a show they stopped inside an episode of, a
// show whose episode they finished with another after it, and a show whose
// last episode they finished.
fn resumes() -> Vec<Resume> {
    vec![
        resume("movie", "movies:1", "Entry 1", reached(600, 5_400, (0, 0))),
        resume(
            "movie",
            "movies:2",
            "Entry 2",
            reached(5_400, 5_400, (0, 0)),
        ),
        resume(
            "series",
            SERIAL,
            "The Serial",
            reached(1_200, 2_760, (1, 2)),
        ),
        resume(
            "series",
            OTHER_SERIAL,
            "Another Serial",
            reached(2_760, 2_760, (1, 2)),
        ),
        resume(
            "series",
            LAST_SERIAL,
            "Last Serial",
            reached(2_760, 2_760, (2, 4)),
        ),
    ]
}

// The browser on a bus, over an audience of one, whose progress store
// answers those five resumes and whose catalog resolves one file to play.
// `orders` says whether the films carry a set and belong to franchises, so
// a finished work has something after it.
fn continuing_with(orders: bool) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.continues = resumes();
    browser.source.sets = orders;
    browser.source.orders = orders;
    if orders {
        browser.source.movies = 5;
    }
    browser.source.reached.insert(
        ("screening/films".to_string(), "movies:1".to_string()),
        reached(600, 5_400, (0, 0)),
    );
    let mut browser = browser.with_audience(
        vec![crate::audience::Person {
            name: "first".into(),
            display_name: "First".into(),
        }],
        vec!["first".into()],
    );
    browser.pump(0.0);
    (browser, bus)
}

// The browser whose catalog holds no set and no franchise, so a work the
// audience finished leaves the row and nothing takes its place.
fn continuing() -> (Browser<Fake, NoArt>, FakeBus) {
    continuing_with(false)
}

// The browser whose catalog holds the set and the three orders, so a work
// the audience finished names the works after it.
fn following() -> (Browser<Fake, NoArt>, FakeBus) {
    continuing_with(true)
}

// The browser with focus on the continue-watching row, the first row under
// the banner.
fn on_the_row() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = continuing();
    browser.key("up");
    (browser, bus)
}

// The continue-watching row's strip.
fn row(browser: &Browser<Fake, NoArt>) -> &crate::screens::home::Strip {
    strip_at(browser, 1)
}

// The captions of the row, in the order it draws them.
fn captions(browser: &Browser<Fake, NoArt>) -> Vec<String> {
    row(browser)
        .items
        .iter()
        .map(|item| item.caption.clone())
        .collect()
}

#[test]
fn the_row_holds_every_work_the_audience_can_go_back_to() {
    let (browser, _bus) = continuing();
    let row = row(&browser);

    assert_eq!(row.heading, "Continue watching");
    let captions: Vec<&str> = row.items.iter().map(|item| item.caption.as_str()).collect();
    assert_eq!(captions, ["Entry 1", "Segment 2", "Segment 3"]);
}

#[test]
fn a_work_the_audience_finished_with_nothing_after_it_leaves_the_row() {
    let (browser, _bus) = continuing();
    let ids: Vec<&str> = row(&browser)
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();

    assert!(!ids.contains(&"movies:2"), "{ids:?}");
    assert_eq!(ids.len(), 3);
}

#[test]
fn a_slot_the_audience_stopped_inside_carries_how_far_they_reached() {
    let (browser, _bus) = continuing();
    let reached: Vec<Option<i64>> = row(&browser)
        .items
        .iter()
        .map(|item| item.progress.map(|played| played.position))
        .collect();

    assert_eq!(reached, [Some(600), Some(1_200), None]);
}

#[test]
fn a_film_of_the_row_reads_as_its_title_over_its_facts() {
    let (browser, _bus) = continuing();
    let film = &row(&browser).items[0];

    assert_eq!(film.library, "screening/films");
    assert_eq!(film.kind, "movies");
    assert_eq!(film.art, "movies:1.jpg");
    assert_eq!(film.under, "1980 · 1h 30m · PG");
    assert_eq!(film.episode, None);
}

#[test]
fn an_episode_of_the_row_reads_as_its_title_over_its_show() {
    let (browser, _bus) = continuing();
    let episode = &row(&browser).items[1];

    assert_eq!(episode.kind, "episodes");
    assert_eq!(episode.id, "episode:1:2");
    assert_eq!(episode.art, "s1e2.jpg");
    assert_eq!(episode.under, "The Serial · S01 · E02 · 46m");
    assert_eq!(
        episode.episode,
        Some(InSeries {
            series: SERIAL.into(),
            name: "The Serial".into(),
            season: 1,
            episode: 2,
        })
    );
}

#[test]
fn a_show_whose_episode_the_audience_finished_stands_at_the_next_one() {
    let (browser, _bus) = continuing();
    let next = &row(&browser).items[2];

    assert_eq!(next.id, "episode:1:3");
    assert_eq!(next.progress, None);
    assert_eq!(
        next.episode,
        Some(InSeries {
            series: OTHER_SERIAL.into(),
            name: "Another Serial".into(),
            season: 1,
            episode: 3,
        })
    );
}

#[test]
fn the_row_is_read_for_the_people_in_the_room() {
    let (browser, _bus) = continuing();

    assert_eq!(browser.source.watching, ["first"]);
}

#[test]
fn a_film_the_audience_finished_is_followed_by_its_set_and_its_franchises() {
    let (browser, _bus) = following();

    assert_eq!(
        captions(&browser),
        [
            "Entry 1",
            "Entry 3",
            "Last Serial",
            "Segment 2",
            "Segment 3",
            "Entry 4",
        ]
    );
}

#[test]
fn a_work_the_set_and_a_franchise_both_name_takes_one_card() {
    let (browser, _bus) = following();
    let ids: Vec<&str> = row(&browser)
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();

    assert_eq!(ids.iter().filter(|id| **id == "movies:3").count(), 1);
}

#[test]
fn a_work_the_audience_is_in_the_middle_of_is_never_drawn_as_a_successor() {
    let (browser, _bus) = following();
    let ids: Vec<&str> = row(&browser)
        .items
        .iter()
        .map(|item| item.id.as_str())
        .collect();

    assert_eq!(ids.iter().filter(|id| **id == "movies:1").count(), 1);
    assert!(row(&browser).items[0].progress.is_some());
}

#[test]
fn a_work_the_audience_has_not_started_carries_no_bar() {
    let (browser, _bus) = following();
    let reached: Vec<Option<i64>> = row(&browser)
        .items
        .iter()
        .map(|item| item.progress.map(|played| played.position))
        .collect();

    assert_eq!(reached, [Some(600), None, None, Some(1_200), None, None]);
}

#[test]
fn a_show_that_follows_a_film_draws_as_a_show() {
    let (browser, _bus) = following();
    let show = &row(&browser).items[2];

    assert_eq!(show.kind, "series");
    assert_eq!(show.id, LAST_SERIAL);
    assert_eq!(show.episode, None);
}

#[test]
fn a_show_whose_last_episode_the_audience_finished_is_followed_by_its_franchise() {
    let (browser, _bus) = following();
    let after = &row(&browser).items[5];

    assert_eq!(after.kind, "movies");
    assert_eq!(after.id, "movies:4");
    assert_eq!(after.caption, "Entry 4");
}

#[test]
fn a_press_on_a_film_of_the_row_opens_its_page_on_resume() {
    let (mut browser, bus) = on_the_row();

    browser.key("enter");

    let page = showing_page(&browser);
    assert_eq!(page.id, "movies:1");
    assert_eq!(page.library, "screening/films");
    assert_eq!(page.buttons()[0].word(), "Resume");
    assert_eq!(page.focus, Focus::Buttons(0));
    assert_eq!(
        page.progress.as_ref().map(|reached| reached.position),
        Some(600)
    );
    assert!(published_nothing(&bus));
}

#[test]
fn a_press_on_an_episode_of_the_row_opens_the_series_page_on_that_episode() {
    let (mut browser, bus) = on_the_row();
    browser.key("right");

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, SERIAL);
    assert_eq!(page.library, SERIALS);
    assert_eq!(page.focus, SeriesFocus::Still(1));
    assert_eq!(page.stills[1].fitted, "Segment 2");
    assert!(published_nothing(&bus));
}

#[test]
fn a_press_on_an_episode_the_audience_has_not_started_opens_the_series_page_on_it() {
    let (mut browser, bus) = on_the_row();
    browser.key("right");
    browser.key("right");

    browser.key("enter");

    let page = showing_series(&browser);
    assert_eq!(page.id, OTHER_SERIAL);
    assert_eq!(page.focus, SeriesFocus::Still(2));
    assert!(published_nothing(&bus));
}

#[test]
fn a_row_the_store_answers_nothing_for_holds_nothing() {
    let (mut browser, _bus) = playing(Vec::new());
    browser.pump(0.0);

    assert!(row(&browser).items.is_empty());
    assert_eq!(row(&browser).heading, "Continue watching");
}
