// The rules this crate holds, each one proved with no broker and no thread.
// They are the tests `media-operator`'s idle command pod carried while one
// process per unit held these rules, ported onto the pure core.

use super::*;
use crate::status::Activity;

const PLAYER: &str = "theater";
const STATUS: &str = "liken/media/players/house/theater/status";
const VOLUME: &str = "liken/media/players/house/theater/volume";
const VOLUME_OWNER: &str = "liken/media/players/house/theater/volume/owner";
const COMMANDS: &str = "liken/media/players/house/theater/commands";
const PANEL: &str = "liken/media/players/house/theater/panel";
const POWER: &str = "liken/media/players/house/theater/power";
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
        volume_owner_topic: Some(VOLUME_OWNER.into()),
        commands_topic: COMMANDS.into(),
        panel_topic: PANEL.into(),
        remotes: vec![Remote {
            events: SOFA_EVENTS.into(),
            focus: SOFA_FOCUS.into(),
        }],
        ..Wiring::default()
    }
}

/// The same unit with its screen wired through a Receiver, so a power press
/// publishes a toggle on the bus instead of reaching the client. A unit
/// without one keeps the shade on a power press, the way it does today.
fn powered() -> Wiring {
    Wiring {
        power_topic: POWER.into(),
        ..wiring()
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
/// already caught up, and with the retained on desire read. That is the
/// state a running client holds once the retained marks and the desire
/// arrive, so a test about the quiet window presses a controller that points
/// here and says nothing about focus or the panel.
fn focused(wiring: &Wiring) -> Screen {
    let mut screen = Screen::new(wiring);
    for mark in &mut screen.marks {
        *mark = Mark {
            player: PLAYER.into(),
            caught_up: true,
            ..Mark::default()
        };
    }
    screen.desire = Some(crate::panel::ON);
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
            VOLUME_OWNER,
            COMMANDS,
            PANEL,
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
        volume_owner_topic: None,
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [STATUS, COMMANDS, PANEL, SOFA_EVENTS, SOFA_FOCUS]
    );
}

#[test]
fn a_unit_whose_operator_named_no_owner_topic_subscribes_to_no_mark() {
    let wiring = Wiring {
        volume_owner_topic: None,
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [STATUS, VOLUME, COMMANDS, PANEL, SOFA_EVENTS, SOFA_FOCUS]
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
        [STATUS, VOLUME, VOLUME_OWNER, COMMANDS, PANEL, SOFA_EVENTS]
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

// The client's own topics.

/// One topic a client owns, such as the mark the library layer's browser
/// keeps for the person watching. The crate reads nothing in it.
const WATCHING: &str = "liken/library/screens/house/theater/watching";

#[test]
fn a_client_subscribes_to_its_own_topics_beside_the_screens() {
    assert_eq!(
        Screen::new(&wiring()).reading(&[WATCHING.into()]).filters(),
        [
            STATUS,
            VOLUME,
            VOLUME_OWNER,
            COMMANDS,
            PANEL,
            SOFA_EVENTS,
            SOFA_FOCUS,
            WATCHING
        ]
    );
}

#[test]
fn a_client_topic_with_no_name_is_no_topic() {
    assert_eq!(
        Screen::new(&wiring())
            .reading(&[String::new(), WATCHING.into()])
            .filters(),
        [
            STATUS,
            VOLUME,
            VOLUME_OWNER,
            COMMANDS,
            PANEL,
            SOFA_EVENTS,
            SOFA_FOCUS,
            WATCHING
        ]
    );
}

#[test]
fn a_client_with_topics_of_its_own_and_no_wiring_still_subscribes() {
    assert_eq!(
        Screen::new(&Wiring::default())
            .reading(&[WATCHING.into()])
            .filters(),
        [WATCHING]
    );
}

#[test]
fn every_message_on_a_client_topic_reaches_the_client() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now).reading(&[WATCHING.into()]);

    assert_eq!(
        moments(screen.deliver(WATCHING, b"person-a", true, now)),
        [Moment::Message {
            topic: WATCHING.into(),
            payload: b"person-a".to_vec(),
            retained: true,
        }]
    );
    // The broker's mark travels with the message, so a client tells its own
    // catch-up from a live write by another session.
    assert_eq!(
        moments(screen.deliver(WATCHING, b"", false, now)),
        [Moment::Message {
            topic: WATCHING.into(),
            payload: Vec::new(),
            retained: false,
        }]
    );
}

#[test]
fn a_client_topic_that_is_also_the_screens_fires_the_screens_rule_alone() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now).reading(&[STATUS.into()]);

    assert_eq!(
        moments(screen.deliver(STATUS, &status("Playing"), true, now)),
        [drew_status(Activity::Playing)]
    );
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
fn a_press_this_crate_hands_the_client_states_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        publishes(screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now)).is_empty()
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

// Which keys reach the client, and which ones this crate answers itself.

#[test]
fn every_key_this_crate_acts_on_no_further_reaches_the_client() {
    let now = Instant::now();
    for name in [
        "KEY_UP",
        "KEY_DOWN",
        "KEY_LEFT",
        "KEY_RIGHT",
        "KEY_ENTER",
        "KEY_OK",
        "KEY_SELECT",
        "KEY_KPENTER",
        "KEY_A",
        "KEY_1",
        "KEY_SPACE",
        "KEY_BACKSPACE",
        "KEY_HOMEPAGE",
        "KEY_SEARCH",
        "KEY_PLAYPAUSE",
    ] {
        for value in [1, 2] {
            let mut screen = idling(&wiring(), now);
            assert_eq!(
                moments(screen.deliver(SOFA_EVENTS, &key(name, value), false, now)),
                [Moment::Press(name.into())],
                "{name} at value {value}"
            );
        }
    }
}

#[test]
fn the_keys_this_crate_answers_itself_reach_no_client() {
    let now = Instant::now();
    for name in [keys::CYCLE, "KEY_VOLUMEUP", "KEY_VOLUMEDOWN", "KEY_MUTE"] {
        for value in [1, 2] {
            let mut screen = idling(&wiring(), now);
            assert!(
                moments(screen.deliver(SOFA_EVENTS, &key(name, value), false, now)).is_empty(),
                "{name} at value {value}"
            );
        }
    }
}

#[test]
fn a_release_reaches_no_client() {
    let now = Instant::now();
    for name in ["KEY_UP", "KEY_A", "KEY_HOMEPAGE", "KEY_BACKSPACE"] {
        let mut screen = idling(&wiring(), now);

        assert!(
            screen
                .deliver(SOFA_EVENTS, &key(name, 0), false, now)
                .is_empty(),
            "{name}"
        );
    }
}

#[test]
fn a_back_press_reaches_the_client_and_leaves_the_shade_up() {
    let now = Instant::now();
    for name in keys::BACK {
        let mut screen = idling(&wiring(), now);
        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)),
            [Moment::Press(name.into())]
        );
        assert!(!screen.asleep);
    }
}

// The power press, for a unit whose screen is wired through a Receiver.

#[test]
fn a_power_press_publishes_the_toggle_and_reaches_no_client() {
    let now = Instant::now();
    for name in keys::POWER {
        let mut screen = idling(&powered(), now);

        let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

        assert!(moments(effects.clone()).is_empty(), "{name}");
        assert_eq!(publishes(effects), only_the_toggle(), "{name}");
    }
}

#[test]
fn a_power_repeat_publishes_no_toggle() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_POWER", 2), false, now)
            .is_empty()
    );
}

#[test]
fn a_power_press_on_a_unit_with_no_receiver_reaches_the_client() {
    let now = Instant::now();
    for name in keys::POWER {
        let mut screen = idling(&wiring(), now);

        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)),
            [Moment::Press(name.into())],
            "{name}"
        );
        assert!(publishes(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)).is_empty());
    }
}

/// The toggle a power press publishes, and nothing beside it.
fn only_the_toggle() -> Vec<Publish> {
    vec![Publish {
        topic: POWER.into(),
        payload: br#"{"action":"toggle"}"#.to_vec(),
        retained: false,
    }]
}

// The equipment operator reads the toggle and decides whether the room goes
// off or on. A shade that woke on the same press would state the on desire,
// the session would turn awake, and that wake would cancel the standby the
// press asked for. So the shade and the desire stand as they were.
#[test]
fn a_power_press_on_a_sleeping_screen_publishes_only_the_toggle() {
    let now = Instant::now();
    for desire in [crate::panel::ON, crate::panel::OFF] {
        for name in keys::POWER {
            let mut screen = idling(&powered(), now);
            screen.asleep = true;
            screen.desire = Some(desire);

            let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

            assert!(moments(effects.clone()).is_empty(), "{name} {desire}");
            assert_eq!(publishes(effects), only_the_toggle(), "{name} {desire}");
            assert!(screen.asleep, "{name} {desire}");
            assert_eq!(screen.desire, Some(desire), "{name} {desire}");
        }
    }
}

#[test]
fn a_power_press_states_no_desire_when_none_was_read() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);
    screen.desire = None;

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    assert_eq!(publishes(effects), only_the_toggle());
    assert_eq!(screen.desire, None);
}

// A power press that turns the room on leaves the screen asleep, and the
// next press wakes it the way any press wakes a sleeping screen.
#[test]
fn the_press_after_a_power_press_wakes_a_sleeping_screen() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);
    screen.asleep = true;
    screen.desire = Some(crate::panel::OFF);
    screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_ENTER", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(
        publishes(effects),
        [Publish {
            topic: PANEL.into(),
            payload: crate::panel::Desire {
                desire: crate::panel::ON
            }
            .payload(),
            retained: true,
        }]
    );
    assert!(!screen.asleep);
}

#[test]
fn a_power_press_on_a_sleeping_unit_with_no_receiver_only_wakes_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert!(publishes(effects).is_empty());
}

#[test]
fn a_power_press_from_an_unfocused_controller_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);
    screen.deliver(SOFA_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_power_press_publishes_no_toggle_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&powered());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now)
            .is_empty()
    );
}

#[test]
fn a_press_on_a_sleeping_screen_only_wakes_it() {
    let now = Instant::now();
    for name in ["KEY_UP", "KEY_A", "KEY_HOMEPAGE", "KEY_POWER"] {
        let mut screen = idling(&wiring(), now);
        screen.asleep = true;

        assert_eq!(
            moments(screen.deliver(SOFA_EVENTS, &key(name, 1), false, now)),
            [Moment::Wake],
            "{name}"
        );
    }
}

#[test]
fn a_press_reaches_no_client_while_a_play_runs() {
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

// The owner mark.

#[test]
fn an_owner_mark_reaches_the_client_as_the_payload_it_carries() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME_OWNER, b"receiver", true, now)),
        [Moment::Owner(b"receiver".to_vec())]
    );
}

#[test]
fn a_cleared_owner_mark_reaches_the_client_empty() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME_OWNER, b"", false, now)),
        [Moment::Owner(Vec::new())]
    );
}

#[test]
fn a_unit_whose_operator_named_no_owner_topic_reads_no_mark() {
    let wiring = Wiring {
        volume_owner_topic: None,
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(
        screen
            .deliver(VOLUME_OWNER, b"receiver", true, now)
            .is_empty()
    );
}

// The focus gate.

#[test]
fn a_live_mark_wakes_the_screen_and_pulses_the_controller_it_named() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.marks[0].player = "cinema".into();
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
        [Moment::Press("KEY_UP".into())]
    );
}

// A publisher that sends the mark again, such as an operator after a
// restart, changes nothing a person did. A wake here lights a dark room.
#[test]
fn a_mark_that_repeats_neither_wakes_nor_pulses() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert!(
        screen
            .deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)
            .is_empty()
    );
    assert!(screen.asleep);
}

// On a controller that one unit lists, the operator answers a cycle with
// the same mark, and that repeat is the press's feedback.
#[test]
fn a_repeat_that_answers_this_clients_cycle_wakes_and_pulses() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Wake, Moment::Focus { remote: 0 }]
    );
    assert!(
        screen
            .deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)
            .is_empty()
    );
}

#[test]
fn a_mark_that_returns_after_another_player_held_it_pulses() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(SOFA_FOCUS, b"cinema", false, now);

    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
}

#[test]
fn the_pulse_carries_the_controllers_place_in_the_spec() {
    let now = Instant::now();
    let mut screen = idling(&two_remotes(), now);
    for mark in &mut screen.marks {
        mark.player = "cinema".into();
    }

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
    screen.marks[0].player = "cinema".into();
    assert_eq!(
        moments(screen.deliver(SOFA_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );

    screen.connected();
    // A mark that moved while the client was away would pulse on a live
    // delivery, so only the catch-up keeps this one quiet.
    screen.marks[0].player = "cinema".into();

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
        [Moment::Press("KEY_UP".into())]
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
        ..Mark::default()
    };

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty()
    );
}

// The commands topic.

/// One ask on the commands topic, as the playback pod's command sidecar
/// publishes it when a person takes the up-next offer on the scrubber.
const ASK: &[u8] = br#"{"action":"play-next","request":{"library":"living-room/shows"}}"#;

#[test]
fn a_play_next_states_the_request_whether_or_not_a_play_runs() {
    let now = Instant::now();
    let mut idle = idling(&wiring(), now);
    let mut playing = focused(&wiring());
    playing.deliver(STATUS, &status("Playing"), true, now);

    for screen in [&mut idle, &mut playing] {
        assert_eq!(
            moments(screen.deliver(COMMANDS, ASK, false, now)),
            [Moment::PlayNext(
                br#"{"library":"living-room/shows"}"#.to_vec()
            )]
        );
    }
}

#[test]
fn a_play_next_with_no_request_states_no_bytes() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(COMMANDS, br#"{"action":"play-next"}"#, false, now)),
        [Moment::PlayNext(Vec::new())]
    );
}

/// The ask the sidecar publishes on a home press during a film.
const HOME_ASK: &[u8] = br#"{"action":"home"}"#;

#[test]
fn a_home_ask_reaches_the_client_as_the_home_key_whether_or_not_a_play_runs() {
    let now = Instant::now();
    let mut idle = idling(&wiring(), now);
    let mut playing = focused(&wiring());
    playing.deliver(STATUS, &status("Playing"), true, now);

    for screen in [&mut idle, &mut playing] {
        assert_eq!(
            moments(screen.deliver(COMMANDS, HOME_ASK, false, now)),
            [Moment::Press(keys::HOME.into())]
        );
    }
}

#[test]
fn a_home_press_while_nothing_plays_reaches_the_client_and_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::HOME, 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Press(keys::HOME.into())]);
    assert!(publishes(effects).is_empty());
}

#[test]
fn every_other_action_and_a_payload_that_does_not_decode_state_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    for payload in [
        &br#"{"action":"pause"}"#[..],
        &br#"{"action":"sleep"}"#[..],
        &b"not json"[..],
        &br#"["play-next"]"#[..],
        &br#"{}"#[..],
    ] {
        assert!(screen.deliver(COMMANDS, payload, false, now).is_empty());
    }
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

mod lines;
mod panel;
