// A play of an episode of a file of several episodes: the request starts at
// the episode's share of the file, and the offer follows the file's last
// episode.

use std::cell::RefCell;
use std::rc::Rc;

use super::*;
use crate::log::Log;

// The fake serial's S01E01 and S01E02 as one double of 5,520 seconds, so
// E02 starts at 2,760 seconds of the file.
fn a_double(duration: i64) -> Vec<((i64, i64), Place)> {
    let part = |part| Place {
        part,
        parts: 2,
        duration,
        last: (1, 2),
    };
    vec![((1, 1), part(1)), ((1, 2), part(2))]
}

// The series page of the fake serial with the double, where the audience
// reached these rows. The page opens on the episode the thread rule offers.
fn on_the_double(duration: i64, reached: Vec<Progress>) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.places = a_double(duration);
    browser.source.episodes_reached = reached;
    browser.source.items = vec![PlayItem {
        start: a_double(duration)[1].1.start(),
        ..one_item()
    }];
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    (browser, bus)
}

// The page with no progress, which opens on E01, moved to E02.
fn on_the_second_episode(duration: i64) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = on_the_double(duration, Vec::new());
    browser.key("right");
    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(1));
    (browser, bus)
}

#[test]
fn play_on_the_second_episode_of_a_double_starts_at_its_midpoint() {
    let (mut browser, bus) = on_the_second_episode(5_520);

    play_episode(&mut browser);

    assert_eq!(published(&bus)["start"], "2760");
}

#[test]
fn resume_on_the_second_episode_starts_at_the_midpoint_plus_its_position() {
    let reached = Progress {
        play: "den#02".into(),
        position: 600,
        duration: 2_760,
        recorded: 10,
        season: 1,
        episode: 2,
        ..Progress::default()
    };
    let (mut browser, bus) = on_the_double(5_520, vec![reached]);
    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(1));

    play_episode(&mut browser);

    assert_eq!(published(&bus)["start"], "3360");
}

#[test]
fn a_double_with_no_duration_starts_at_the_beginning_and_says_why() {
    let (mut browser, bus) = on_the_second_episode(0);
    let (log, lines) = Log::kept();
    browser.log = log;
    let lines: Rc<RefCell<Vec<String>>> = lines;

    play_episode(&mut browser);

    assert_eq!(published(&bus).get("start"), None);
    let lines = lines.take();
    assert!(
        lines.iter().any(|line| line.contains(
            "S1E2 in screening/serials from the start for nobody: starts at the beginning of the \
             file, because the file holds several episodes and has no duration to split"
        )),
        "{lines:?}"
    );
}

// A play of the first episode runs to the end of the double, so the offer
// is the episode after the double.
#[test]
fn the_offer_after_the_first_episode_of_a_double_is_the_episode_after_it() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.places = a_double(5_520);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    play_episode(&mut browser);

    let next = published(&bus)["next"].clone();
    assert_eq!(next["title"], "E03 · Segment 3");
    assert_eq!(next["remaining_seconds"], 2_760);
}

// A play of a later part of a file runs from that part to the end of the
// file, so the offer of it counts those seconds.
#[test]
fn the_offer_of_a_later_part_counts_to_the_end_of_its_file() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.places = vec![(
        (1, 2),
        Place {
            part: 2,
            parts: 2,
            duration: 6_000,
            last: (1, 3),
        },
    )];
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    play_episode(&mut browser);

    let next = published(&bus)["next"].clone();
    assert_eq!(next["title"], "E02 · Segment 2");
    assert_eq!(next["remaining_seconds"], 3_000);
}
