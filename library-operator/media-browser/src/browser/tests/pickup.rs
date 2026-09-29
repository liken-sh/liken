// Pick up here from the browser's side: one retained mark that lists the
// earlier episodes and the later ones it clears, published before the play
// request, and the same play request Play publishes.

use super::*;
use crate::bus::mark::{Episode as Listed, TitleMark};

// The branch the operator names on a screen pod for the namespace's plays,
// and the second every press of these cases lands in.
const PLAYS: &str = "liken/library/plays/house";
const NOW: i64 = 1_759_140_000;

// The one `Person` these cases mark for.
const WATCHER: &str = "first";

// A browser that publishes marks as the `den` screen, over an audience of
// one, on the fake serial's page with the row of the third episode of the
// first season open. The catalog names the serial by one tvdb id.
fn on_the_third_episode() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.identity = Identity {
        aliases: [("tvdb".to_string(), "8001".to_string())].into(),
        ..Identity::default()
    };
    let mut browser = browser
        .with_marks(PLAYS.into(), "den".into())
        .with_audience(
            vec![crate::audience::Person {
                name: WATCHER.into(),
                display_name: "First".into(),
            }],
            vec![WATCHER.into()],
        );
    browser.now = || NOW;
    for key in ["right", "enter", "enter", "right", "right", "enter"] {
        browser.key(key);
    }
    (browser, bus)
}

// Every topic the browser published on, in order.
fn topics(bus: &FakeBus) -> Vec<String> {
    bus.published
        .lock()
        .expect("no test panics with the lock")
        .iter()
        .map(|(topic, _, _)| topic.clone())
        .collect()
}

// The one message the browser published under the plays branch.
fn the_mark(bus: &FakeBus) -> (String, serde_json::Value, bool) {
    let published = bus.published.lock().expect("no test panics with the lock");
    let marks: Vec<_> = published
        .iter()
        .filter(|(topic, _, _)| topic.starts_with(PLAYS))
        .collect();
    assert_eq!(marks.len(), 1, "one press is one mark");
    let (topic, payload, retained) = marks[0];
    (
        topic.clone(),
        serde_json::from_slice(payload).expect("the mark is JSON"),
        *retained,
    )
}

#[test]
fn pick_up_here_publishes_one_retained_list_of_the_earlier_episodes_and_then_the_play() {
    let (mut browser, bus) = on_the_third_episode();
    let series = showing_series(&browser);
    assert_eq!(
        series
            .buttons()
            .iter()
            .map(|button| button.word())
            .collect::<Vec<_>>(),
        ["Play", "Pick up here"]
    );

    browser.key("right");
    browser.key("enter");

    let (topic, message, retained) = the_mark(&bus);
    assert_eq!(topic, format!("{PLAYS}/mark-den-{NOW}/mark"));
    assert!(retained);
    assert_eq!(
        message,
        serde_json::json!({
            "mark": "watched",
            "player": "den",
            "people": [WATCHER],
            "aliases": {"tvdb": "8001"},
            "episodes": [
                {"mark": "watched", "season": 1, "episode": 1, "position": 2760, "duration": 2760},
                {"mark": "watched", "season": 1, "episode": 2, "position": 2760, "duration": 2760},
            ],
            "at": NOW,
        })
    );
    assert_eq!(
        topics(&bus)
            .iter()
            .filter(|topic| topic.starts_with(PLAYS) || *topic == PLAY_TOPIC)
            .cloned()
            .collect::<Vec<_>>(),
        [topic, PLAY_TOPIC.to_string()],
        "the mark goes out before the play request"
    );
}

#[test]
fn pick_up_here_asks_for_the_same_play_as_play() {
    let (mut picked, picked_bus) = on_the_third_episode();
    picked.key("right");
    picked.key("enter");
    let (mut played, played_bus) = on_the_third_episode();
    played.key("enter");

    assert_eq!(published(&picked_bus), published(&played_bus));
    assert!(picked.loading.is_some());
}

// The play of the third episode of the first season, which every press of
// these cases asks for after its mark.
fn the_third() -> Box<Step> {
    Box::new(Step::Play {
        library: SERIALS.into(),
        selection: Selection::Episode {
            series: SERIAL.into(),
            season: 1,
            episode: 3,
        },
        start: None,
        next: None,
    })
}

// A press that clears later episodes carries them in the same one message
// as the earlier ones it marks watched, each with its own mark.
#[test]
fn a_list_that_clears_later_episodes_publishes_them_in_the_same_mark() {
    let (mut browser, bus) = on_the_third_episode();
    let listed = |episode, mark| Listed {
        season: 1,
        episode,
        duration: 2_760,
        mark,
    };

    browser.take(Step::PickUp {
        library: SERIALS.into(),
        series: SERIAL.into(),
        episodes: vec![listed(2, TitleMark::Watched), listed(4, TitleMark::Cleared)],
        play: the_third(),
    });

    let (_, message, retained) = the_mark(&bus);
    assert!(retained);
    assert_eq!(
        message["episodes"],
        serde_json::json!([
            {"mark": "watched", "season": 1, "episode": 2, "position": 2760, "duration": 2760},
            {"mark": "cleared", "season": 1, "episode": 4, "position": 0, "duration": 2760},
        ])
    );
    assert_eq!(requests(&bus).len(), 1);
}

// A list with nothing in it has nothing to mark. The page never asks for
// one, because the button shows only where the press changes an episode.
#[test]
fn an_empty_list_publishes_no_mark_and_still_plays() {
    let (mut browser, bus) = on_the_third_episode();

    browser.take(Step::PickUp {
        library: SERIALS.into(),
        series: SERIAL.into(),
        episodes: Vec::<Listed>::new(),
        play: the_third(),
    });

    assert!(topics(&bus).iter().all(|topic| !topic.starts_with(PLAYS)));
    assert_eq!(requests(&bus).len(), 1);
}
