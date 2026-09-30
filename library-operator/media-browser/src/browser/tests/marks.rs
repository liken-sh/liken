// The marks a person sets from a page: the retained message the browser
// publishes for everyone at the screen, on a topic of its own for each
// press.

use super::*;
use crate::screens::TitleMark;
use crate::screens::movie::watch::Mark;

// The branch the operator names on a screen pod for the namespace's plays,
// and the second every mark of these cases is pressed at.
const PLAYS: &str = "liken/library/plays/house";
const NOW: i64 = 1_759_140_000;

// The one `Person` these cases mark for.
const WATCHER: &str = "first";

// A browser on a bus that publishes marks as the `den` screen, over an
// audience of one, whose catalog names every work by one tmdb id.
fn marking() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.identity = Identity {
        aliases: [("tmdb".to_string(), "7001".to_string())].into(),
        ..Identity::default()
    };
    let mut browser = browser
        .with_marks(PLAYS.into(), "den".into())
        .with_audience(
            vec![crate::audience::Person {
                name: WATCHER.into(),
                display_name: "First".into(),
                thumbnail: None,
            }],
            vec![WATCHER.into()],
        );
    browser.now = || NOW;
    (browser, bus)
}

// Every message the browser published under the plays branch, in order.
fn marks(bus: &FakeBus) -> Vec<(String, serde_json::Value, bool)> {
    bus.published
        .lock()
        .expect("no test panics with the lock")
        .iter()
        .filter(|(topic, _, _)| topic.starts_with(PLAYS))
        .map(|(topic, payload, retained)| {
            let message = serde_json::from_slice(payload).expect("the mark is JSON");
            (topic.clone(), message, *retained)
        })
        .collect()
}

// Put focus on one mark of the page on top, down from the playback row,
// and press it.
fn press_mark(browser: &mut Browser<Fake, NoArt>, mark: Mark) {
    let index = showing_page(browser)
        .marks()
        .iter()
        .position(|held| *held == mark)
        .expect("the status line holds the mark");
    browser.key("down");
    for _ in 0..index {
        browser.key("right");
    }
    browser.key("enter");
}

#[test]
fn mark_watched_on_a_films_page_publishes_a_retained_mark_for_the_room() {
    let (mut browser, bus) = marking();
    browser.key("enter");
    browser.key("enter");

    press_mark(&mut browser, Mark::Watched);

    assert_eq!(
        marks(&bus),
        [(
            format!("{PLAYS}/mark-den-{NOW}/mark"),
            serde_json::json!({
                "mark": "watched",
                "player": "den",
                "people": [WATCHER],
                "aliases": {"tmdb": "7001"},
                "season": 0,
                "episode": 0,
                "position": 5400,
                "duration": 5400,
                "at": NOW,
            }),
            true,
        )]
    );
    assert!(published_nothing(&bus));
}

#[test]
fn clear_progress_on_an_episode_publishes_position_zero_with_its_numbers() {
    let (mut browser, bus) = marking();
    browser.source.episodes_reached = vec![Progress {
        play: "play-1".into(),
        position: 900,
        duration: 2_760,
        recorded: 10,
        season: 1,
        episode: 1,
        ..Progress::default()
    }];
    browser.source.identity.season = 1;
    browser.source.identity.episode = 1;
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("right");

    browser.key("enter");

    let sent = marks(&bus);
    assert_eq!(sent.len(), 1);
    let (_, message, _) = &sent[0];
    assert_eq!(message["mark"], "cleared");
    assert_eq!(message["position"], 0);
    assert_eq!(message["duration"], 2760);
    assert_eq!(
        (&message["season"], &message["episode"]),
        (&1.into(), &1.into())
    );
    assert!(matches!(
        showing_series(&browser).focus,
        SeriesFocus::Still(0)
    ));
}

#[test]
fn a_second_mark_in_the_same_second_names_the_next_second() {
    let (mut browser, bus) = marking();
    browser.key("enter");
    browser.key("enter");
    press_mark(&mut browser, Mark::Watched);
    browser.take(Step::Mark {
        library: "screening/films".into(),
        selection: Selection::Movie {
            id: "movies:1".into(),
        },
        mark: TitleMark::Cleared,
        duration: 5_400,
    });

    let topics: Vec<String> = marks(&bus).into_iter().map(|(topic, _, _)| topic).collect();

    assert_eq!(
        topics,
        [
            format!("{PLAYS}/mark-den-{NOW}/mark"),
            format!("{PLAYS}/mark-den-{}/mark", NOW + 1),
        ]
    );
}

#[test]
fn a_watched_mark_on_a_work_with_no_duration_sends_nothing() {
    let (mut browser, bus) = marking();

    browser.take(Step::Mark {
        library: "screening/films".into(),
        selection: Selection::Movie {
            id: "movies:1".into(),
        },
        mark: TitleMark::Watched,
        duration: 0,
    });

    assert!(marks(&bus).is_empty());
}

#[test]
fn a_browser_the_operator_named_no_branch_for_sends_no_mark() {
    let (browser, bus) = playing(vec![one_item()]);
    let mut browser = browser.with_marks(String::new(), "den".into());

    browser.take(Step::Mark {
        library: "screening/films".into(),
        selection: Selection::Movie {
            id: "movies:1".into(),
        },
        mark: TitleMark::Cleared,
        duration: 5_400,
    });

    assert!(
        bus.published
            .lock()
            .expect("no test panics with the lock")
            .is_empty()
    );
}
