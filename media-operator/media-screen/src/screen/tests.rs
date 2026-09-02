// The rules this crate holds, each one proved with no broker and no thread.
// They are the tests `media-operator`'s idle command pod carried while one
// process per unit held these rules, ported onto the pure core.

use super::*;
use crate::status::Activity;

const PLAYER: &str = "theater";
const STATUS: &str = "liken/media/players/house/theater/status";
const VOLUME: &str = "liken/media/players/house/theater/volume";
const COMMANDS: &str = "liken/media/players/house/theater/commands";
const PANEL: &str = "liken/media/players/house/theater/panel";
const SOFA_EVENTS: &str = "liken/media/remotes/house/sofa/events";
const SOFA_FOCUS: &str = "liken/media/remotes/house/sofa/focus";
const ARMCHAIR_EVENTS: &str = "liken/media/remotes/house/armchair/events";
const ARMCHAIR_FOCUS: &str = "liken/media/remotes/house/armchair/focus";

/// One unit with one controller, no windows, and speakers.
fn wiring() -> Wiring {
    Wiring {
        player_name: PLAYER.into(),
        status_topic: STATUS.into(),
        volume_topic: VOLUME.into(),
        commands_topic: COMMANDS.into(),
        panel_topic: PANEL.into(),
        remotes: vec![Remote {
            events: SOFA_EVENTS.into(),
            focus: SOFA_FOCUS.into(),
        }],
        ..Wiring::default()
    }
}

/// The same unit with a second controller, so a test reads the index a focus
/// carries for a controller that is not the first. The order is the order
/// `spec.remotes` states.
fn two_remotes() -> Wiring {
    Wiring {
        remotes: vec![
            Remote {
                events: ARMCHAIR_EVENTS.into(),
                focus: ARMCHAIR_FOCUS.into(),
            },
            Remote {
                events: SOFA_EVENTS.into(),
                focus: SOFA_FOCUS.into(),
            },
        ],
        ..wiring()
    }
}

/// A screen whose controllers all hold this unit's mark, with each mark
/// already caught up. That is the state a running client holds once the
/// retained marks arrive, so a test about the quiet window presses a
/// controller that points here and says nothing about focus.
fn focused(wiring: &Wiring) -> Screen {
    let mut screen = Screen::new(wiring);
    for mark in &mut screen.marks {
        *mark = Mark {
            player: PLAYER.into(),
            caught_up: true,
        };
    }
    screen
}

/// The same screen with the unit's retained status already read, so it plays
/// nothing and the windows are armed.
fn idling(wiring: &Wiring, now: Instant) -> Screen {
    let mut screen = focused(wiring);
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen
}

/// One status off the unit's retained status topic, the way the operator
/// publishes it.
fn status(activity: &str) -> Vec<u8> {
    format!(r#"{{"displayName":"The Theater","activity":"{activity}"}}"#).into_bytes()
}

/// One key event on a controller's events topic, the way the standing remote
/// pod publishes it.
fn key(name: &str, value: i64) -> Vec<u8> {
    format!(r#"{{"key":"{name}","value":{value}}}"#).into_bytes()
}

/// What the client draws out of one fold.
fn moments(effects: Vec<Effect>) -> Vec<Moment> {
    effects
        .into_iter()
        .filter_map(|effect| match effect {
            Effect::Moment(moment) => Some(moment),
            Effect::Publish(_) => None,
        })
        .collect()
}

/// What the crate sends out of one fold.
fn publishes(effects: Vec<Effect>) -> Vec<Publish> {
    effects
        .into_iter()
        .filter_map(|effect| match effect {
            Effect::Publish(publish) => Some(publish),
            Effect::Moment(_) => None,
        })
        .collect()
}

/// The status moment every status carries, so a test about the shade names
/// what it expects beside it.
fn drew_status(activity: Activity) -> Moment {
    Moment::Status(Status {
        display_name: "The Theater".into(),
        activity,
        ..Status::default()
    })
}

// The topics a client subscribes to.

#[test]
fn the_screen_subscribes_to_every_topic_the_operator_named() {
    assert_eq!(
        Screen::new(&two_remotes()).filters(),
        [
            STATUS,
            VOLUME,
            COMMANDS,
            ARMCHAIR_EVENTS,
            ARMCHAIR_FOCUS,
            SOFA_EVENTS,
            SOFA_FOCUS
        ]
    );
}

#[test]
fn a_unit_with_no_sinks_subscribes_to_no_level() {
    let wiring = Wiring {
        volume_topic: String::new(),
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [STATUS, COMMANDS, SOFA_EVENTS, SOFA_FOCUS]
    );
}

#[test]
fn a_controller_with_no_focus_topic_subscribes_to_no_mark() {
    let wiring = Wiring {
        remotes: vec![Remote {
            events: SOFA_EVENTS.into(),
            focus: String::new(),
        }],
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [STATUS, VOLUME, COMMANDS, SOFA_EVENTS]
    );
}

#[test]
fn a_topic_this_screen_did_not_subscribe_to_is_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    assert!(
        screen
            .deliver("liken/media/plays/house/friday/status", b"{}", true, now)
            .is_empty()
    );
    assert!(screen.deliver("", b"{}", true, now).is_empty());
}

// The status, and the play gate it sets.

#[test]
fn every_status_reaches_the_client() {
    let now = Instant::now();
    let mut screen = focused(&wiring());

    assert_eq!(
        moments(screen.deliver(STATUS, &status("Playing"), true, now)),
        [drew_status(Activity::Playing)]
    );
}

#[test]
fn a_status_that_does_not_decode_changes_nothing() {
    let now = Instant::now();
    let mut screen = focused(&wiring());

    assert!(screen.deliver(STATUS, b"not json", true, now).is_empty());
    assert!(!screen.idle);
}

#[test]
fn a_status_that_leaves_idle_wakes_a_sleeping_screen() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(STATUS, &status("Starting"), true, now)),
        [drew_status(Activity::Starting), Moment::Wake]
    );
}

#[test]
fn a_republished_status_does_not_restart_the_quiet_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    assert_eq!(screen.next_deadline(), Some(now + Duration::from_secs(600)));

    let later = now + Duration::from_secs(120);
    screen.deliver(STATUS, &status("Idle"), true, later);

    assert_eq!(screen.next_deadline(), Some(now + Duration::from_secs(600)));
}

// The quiet window and the shade.

#[test]
fn a_unit_that_plays_nothing_arms_the_quiet_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(screen.tick(now + Duration::from_secs(599)).is_empty());
    assert_eq!(
        moments(screen.tick(now + Duration::from_secs(600))),
        [Moment::Sleep]
    );
    assert!(screen.asleep);
}

#[test]
fn the_quiet_window_never_arms_while_a_play_runs() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = focused(&wiring);

    screen.deliver(STATUS, &status("Playing"), true, now);

    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_quiet_window_of_zero_never_arms() {
    let now = Instant::now();
    let screen = idling(&wiring(), now);

    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_press_restarts_the_quiet_window_from_the_press() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    let later = now + Duration::from_secs(500);
    screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, later);

    assert_eq!(
        screen.next_deadline(),
        Some(later + Duration::from_secs(600))
    );
}

#[test]
fn a_press_wakes_a_sleeping_screen_and_does_nothing_else() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now)),
        [Moment::Wake]
    );
    assert!(!screen.asleep);
}

#[test]
fn a_key_this_crate_hands_no_client_states_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now)
            .is_empty()
    );
}

#[test]
fn an_event_that_does_not_decode_neither_wakes_nor_restarts_the_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.asleep = true;
    screen.deadline = None;

    assert!(
        screen
            .deliver(SOFA_EVENTS, b"not json", false, now)
            .is_empty()
    );
    assert!(screen.asleep);
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_release_wakes_nothing_and_restarts_nothing() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.asleep = true;
    screen.deadline = None;

    let later = now + Duration::from_secs(120);
    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_BACK", 0), false, later)
            .is_empty()
    );
    assert!(screen.asleep);
    assert_eq!(screen.next_deadline(), None);
}

// The shade the client asks for, which is what back does under the stock
// client and what the top level does under a client with levels.

#[test]
fn the_client_brings_the_shade_down_at_once() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(moments(screen.sleep(now)), [Moment::Sleep]);
    assert!(screen.asleep);
}

#[test]
fn the_shade_the_client_asked_for_starts_the_off_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    screen.sleep(now);

    assert_eq!(
        screen.next_deadline(),
        Some(now + Duration::from_secs(1200))
    );
}

#[test]
fn the_client_brings_no_shade_down_on_a_screen_that_already_sleeps() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert!(screen.sleep(now).is_empty());
}

#[test]
fn the_client_brings_no_shade_down_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(screen.sleep(now).is_empty());
}

// The navigation keys, which reach the client under the kernel's name.

#[test]
fn every_navigation_key_reaches_the_client() {
    let now = Instant::now();
    for name in keys::NAVIGATION {
        for value in [1, 2] {
            let mut screen = idling(&wiring(), now);
            assert_eq!(
                moments(screen.deliver(SOFA_EVENTS, &key(name, value), false, now)),
                [Moment::Press(name)],
                "{name} at value {value}"
            );
        }
    }
}

#[test]
fn a_release_reaches_no_client() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_UP", 0), false, now)
            .is_empty()
    );
}

#[test]
fn a_back_press_reaches_the_client_and_leaves_the_shade_up() {
    let now = Instant::now();
    for name in keys::BACK {
        let mut screen = idling(&wiring(), now);
        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)),
            [Moment::Press(name)]
        );
        assert!(!screen.asleep);
    }
}

#[test]
fn a_navigation_press_on_a_sleeping_screen_only_wakes_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)),
        [Moment::Wake]
    );
}

#[test]
fn a_navigation_press_reaches_no_client_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_press_from_an_unfocused_controller_reaches_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(SOFA_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty()
    );
}

// The level.

#[test]
fn the_screen_holds_the_level_the_topic_delivered_and_hands_it_to_the_client() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME, br#"{"level":45,"muted":true}"#, true, now)),
        [Moment::Level {
            volume: Volume {
                level: 45,
                muted: true
            },
            pressed: false,
        }]
    );
    assert_eq!(
        screen.volume,
        Some(Volume {
            level: 45,
            muted: true
        })
    );
}

#[test]
fn a_retained_level_is_the_catch_up_and_a_live_level_is_a_press() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME, br#"{"level":45,"muted":false}"#, false, now)),
        [Moment::Level {
            volume: Volume {
                level: 45,
                muted: false
            },
            pressed: true,
        }]
    );
}

#[test]
fn the_screen_keeps_the_level_through_a_message_that_does_not_decode() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(VOLUME, br#"{"level":45,"muted":true}"#, true, now);

    assert!(screen.deliver(VOLUME, b"not json", true, now).is_empty());

    assert_eq!(
        screen.volume,
        Some(Volume {
            level: 45,
            muted: true
        })
    );
}

#[test]
fn a_level_press_steps_from_unity_before_any_message() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEDOWN", 1), false, now)),
        [Publish {
            topic: VOLUME.into(),
            payload: br#"{"level":95,"muted":false}"#.to_vec(),
            retained: true,
        }]
    );
}

#[test]
fn a_level_press_publishes_the_units_next_level_and_draws_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(VOLUME, br#"{"level":40,"muted":false}"#, true, now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(
        publishes(effects),
        [Publish {
            topic: VOLUME.into(),
            payload: br#"{"level":45,"muted":false}"#.to_vec(),
            retained: true,
        }]
    );
}

#[test]
fn a_mute_press_publishes_the_toggled_flag() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_MUTE", 1), false, now))[0].payload,
        br#"{"level":100,"muted":true}"#
    );
}

#[test]
fn a_mute_repeat_toggles_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_MUTE", 2), false, now)
            .is_empty()
    );
}

#[test]
fn a_level_repeat_steps_the_level_again() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(VOLUME, br#"{"level":40,"muted":false}"#, true, now);

    assert_eq!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now))[0].payload,
        br#"{"level":45,"muted":false}"#
    );
    // The press publishes and reads its own level back off the topic, so the
    // repeat that follows steps from the level the topic now holds.
    screen.deliver(VOLUME, br#"{"level":45,"muted":false}"#, false, now);

    assert_eq!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 2), false, now))[0].payload,
        br#"{"level":50,"muted":false}"#
    );
}

#[test]
fn a_level_release_steps_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 0), false, now)
            .is_empty()
    );
}

#[test]
fn a_level_press_publishes_no_level_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    for value in [1, 2] {
        assert!(
            screen
                .deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", value), false, now)
                .is_empty()
        );
    }
}

#[test]
fn a_level_press_on_a_sleeping_screen_only_wakes_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert!(publishes(effects).is_empty());
}

#[test]
fn a_unit_with_no_sinks_answers_no_level_press() {
    let wiring = Wiring {
        volume_topic: String::new(),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_level_press_from_an_unfocused_controller_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(SOFA_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_level_repeat_after_the_mark_moves_away_steps_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    assert_eq!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 1), false, now)).len(),
        1
    );

    screen.deliver(SOFA_FOCUS, b"cinema", false, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_VOLUMEUP", 2), false, now)
            .is_empty()
    );
}

// The focus gate.

#[test]
fn a_live_mark_wakes_the_screen_and_pulses_the_controller_it_named() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.marks[0].caught_up = true;
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Wake, Moment::Focus { remote: 0 }]
    );
}

#[test]
fn the_retained_catch_up_neither_wakes_nor_pulses() {
    let now = Instant::now();
    let mut screen = Screen::new(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.asleep = true;

    assert!(
        screen
            .deliver(SOFA_FOCUS, PLAYER.as_bytes(), true, now)
            .is_empty()
    );
    assert!(screen.asleep);
}

#[test]
fn the_retained_catch_up_still_opens_the_gate() {
    let now = Instant::now();
    let mut screen = Screen::new(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), true, now);

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)),
        [Moment::Press("KEY_UP")]
    );
}

#[test]
fn a_mark_that_repeats_pulses_again() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
}

#[test]
fn the_pulse_carries_the_controllers_place_in_the_spec() {
    let now = Instant::now();
    let mut screen = idling(&two_remotes(), now);

    assert_eq!(
        moments(screen.deliver(ARMCHAIR_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 1 }]
    );
}

#[test]
fn a_mark_that_names_another_player_pulses_nothing() {
    let now = Instant::now();
    for mark in ["cinema", "friday-film", ""] {
        let mut screen = idling(&wiring(), now);
        assert!(
            screen
                .deliver(SOFA_FOCUS, mark.as_bytes(), false, now)
                .is_empty()
        );
    }
}

#[test]
fn a_client_that_read_no_player_name_matches_no_mark() {
    let wiring = Wiring {
        player_name: String::new(),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(screen.deliver(SOFA_FOCUS, b"", false, now).is_empty());
    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_bus_session_makes_the_next_mark_a_catch_up_again() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );

    screen.connected();

    assert!(
        screen
            .deliver(SOFA_FOCUS, PLAYER.as_bytes(), true, now)
            .is_empty()
    );
}

#[test]
fn the_mark_stands_across_a_bus_session() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.connected();

    assert_eq!(
        moments(screen.deliver(SOFA_EVENTS, &key("KEY_UP", 1), false, now)),
        [Moment::Press("KEY_UP")]
    );
}

// The cycle request.

#[test]
fn the_cycle_key_publishes_the_cycle_request_and_nothing_else() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(
        publishes(effects),
        [Publish {
            topic: format!("{SOFA_FOCUS}/cycle"),
            payload: Vec::new(),
            retained: false,
        }]
    );
}

#[test]
fn the_cycle_key_publishes_nothing_from_an_unfocused_controller() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(SOFA_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty()
    );
}

#[test]
fn the_cycle_key_leaves_a_sleeping_screen_asleep() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert!(moments(effects).is_empty());
    assert!(screen.asleep);
}

#[test]
fn the_cycle_key_publishes_nothing_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_controller_with_no_focus_topic_publishes_no_cycle() {
    let wiring = Wiring {
        remotes: vec![Remote {
            events: SOFA_EVENTS.into(),
            focus: String::new(),
        }],
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    // The mark is the gate, and a controller with no focus topic never
    // carries one, so the gate is opened here by hand to reach the cycle.
    screen.marks[0] = Mark {
        player: PLAYER.into(),
        caught_up: true,
    };

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty()
    );
}

// The commands topic.

#[test]
fn a_re_present_states_the_present_moment() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(COMMANDS, br#"{"action":"re-present"}"#, false, now)),
        [Moment::Present]
    );
}

#[test]
fn a_re_present_states_nothing_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(COMMANDS, br#"{"action":"re-present"}"#, false, now)
            .is_empty()
    );
}

#[test]
fn every_other_action_and_a_payload_that_does_not_decode_state_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    for payload in [
        &br#"{"action":"pause"}"#[..],
        &br#"{"action":"sleep"}"#[..],
        &b"not json"[..],
        &br#"["re-present"]"#[..],
        &br#"{}"#[..],
    ] {
        assert!(screen.deliver(COMMANDS, payload, false, now).is_empty());
    }
}

// The panel desire.

#[test]
fn every_bus_session_states_the_desire_the_client_holds() {
    let mut screen = Screen::new(&wiring());

    assert_eq!(
        publishes(screen.connected()),
        [Publish {
            topic: PANEL.into(),
            payload: br#"{"desire":"on"}"#.to_vec(),
            retained: true,
        }]
    );
}

#[test]
fn a_player_with_no_panel_topic_states_no_desire() {
    let wiring = Wiring {
        panel_topic: String::new(),
        ..wiring()
    };
    let mut screen = Screen::new(&wiring);

    assert!(screen.connected().is_empty());
}

#[test]
fn the_off_window_states_the_off_desire_behind_a_black_screen() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    let dark = now + Duration::from_secs(600);
    assert_eq!(moments(screen.tick(dark)), [Moment::Sleep]);
    // The off window runs from the moment the shade came down, so the two
    // windows measure one quiet stretch and nothing states a desire between
    // them.
    assert!(screen.tick(now + Duration::from_secs(1799)).is_empty());
    assert_eq!(
        publishes(screen.tick(now + Duration::from_secs(1800))),
        [Publish {
            topic: PANEL.into(),
            payload: br#"{"desire":"off"}"#.to_vec(),
            retained: true,
        }]
    );
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn an_off_window_of_zero_never_darkens_the_panel() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    screen.tick(now + Duration::from_secs(600));

    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_press_states_the_on_desire_and_relights_the_panel() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));

    let pressed = now + Duration::from_secs(2000);
    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, pressed);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects)[0].payload, br#"{"desire":"on"}"#);
    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
}

#[test]
fn a_starting_play_states_the_on_desire() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));

    let effects = screen.deliver(STATUS, &status("Starting"), true, now);

    assert_eq!(publishes(effects)[0].payload, br#"{"desire":"on"}"#);
}

#[test]
fn a_press_inside_the_off_window_keeps_the_panel_lit() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));

    let pressed = now + Duration::from_secs(1700);
    screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, pressed);

    // The press woke the screen, so the window armed now is the quiet one and
    // no desire went out at all.
    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
    assert!(screen.tick(now + Duration::from_secs(1800)).is_empty());
    assert_eq!(screen.desire, panel::ON);
}

#[test]
fn a_dark_panel_arms_no_second_off_window() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));
    assert_eq!(screen.desire, panel::OFF);

    // A status that repeats the activity leaves the windows where they
    // stand, so the rearm here is the one a live mark for another unit makes.
    screen.deliver(
        SOFA_FOCUS,
        b"cinema",
        false,
        now + Duration::from_secs(1900),
    );

    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_tick_before_the_deadline_and_a_tick_with_no_deadline_state_nothing() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = focused(&wiring);

    assert!(screen.tick(now).is_empty());

    screen.deliver(STATUS, &status("Idle"), true, now);
    assert!(screen.tick(now + Duration::from_secs(1)).is_empty());
}
