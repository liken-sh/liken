use super::*;
use crate::status::Activity;

const PLAYER: &str = "player-1";
const STATUS: &str = "liken/media/players/default/player-1/status";
const VOLUME: &str = "liken/media/players/default/player-1/volume";
const COMMANDS: &str = "liken/media/players/default/player-1/commands";
const PANEL: &str = "liken/media/players/default/player-1/panel";
const POWER: &str = "liken/media/players/default/player-1/power";
const FIRST_REMOTE_EVENTS: &str = "liken/media/remotes/default/remote-1/events";
const FIRST_REMOTE_FOCUS: &str = "liken/media/remotes/default/remote-1/focus";
const SECOND_REMOTE_EVENTS: &str = "liken/media/remotes/default/remote-2/events";
const SECOND_REMOTE_FOCUS: &str = "liken/media/remotes/default/remote-2/focus";

/// A `Player` with one controller and speakers, whose screen never sleeps
/// and whose panel never turns off.
fn wiring() -> Wiring {
    Wiring {
        player_name: PLAYER.into(),
        status_topic: STATUS.into(),
        volume_topic: VOLUME.into(),
        commands_topic: COMMANDS.into(),
        panel_topic: PANEL.into(),
        power_topic: POWER.into(),
        remotes: vec![Remote {
            events: FIRST_REMOTE_EVENTS.into(),
            focus: FIRST_REMOTE_FOCUS.into(),
        }],
        ..Wiring::default()
    }
}

/// The same `Player` with two controllers, in `spec.remotes` order.
fn two_remotes() -> Wiring {
    Wiring {
        remotes: vec![
            Remote {
                events: SECOND_REMOTE_EVENTS.into(),
                focus: SECOND_REMOTE_FOCUS.into(),
            },
            Remote {
                events: FIRST_REMOTE_EVENTS.into(),
                focus: FIRST_REMOTE_FOCUS.into(),
            },
        ],
        ..wiring()
    }
}

/// A screen in the state of a running client after the retained messages
/// arrive: every controller's focus message names this `Player`, and the panel
/// is `on`.
fn focused(wiring: &Wiring) -> Screen {
    let mut screen = Screen::new(wiring);
    for focus in &mut screen.focus {
        *focus = FocusMessage {
            player: PLAYER.into(),
            received_this_session: true,
            ..FocusMessage::default()
        };
    }
    screen.panel_request = Some(crate::panel::ON);
    screen
}

/// The same screen after an `Idle` status, which starts the timers.
fn idling(wiring: &Wiring, now: Instant) -> Screen {
    let mut screen = focused(wiring);
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen
}

fn status(activity: &str) -> Vec<u8> {
    format!(r#"{{"displayName":"Player One","activity":"{activity}"}}"#).into_bytes()
}

fn key(name: &str, value: i64) -> Vec<u8> {
    format!(r#"{{"key":"{name}","value":{value}}}"#).into_bytes()
}

fn moments(effects: Vec<Effect>) -> Vec<Moment> {
    effects
        .into_iter()
        .filter_map(|effect| match effect {
            Effect::Moment(moment) => Some(moment),
            Effect::Publish(_) => None,
        })
        .collect()
}

fn publishes(effects: Vec<Effect>) -> Vec<Publish> {
    effects
        .into_iter()
        .filter_map(|effect| match effect {
            Effect::Publish(publish) => Some(publish),
            Effect::Moment(_) => None,
        })
        .collect()
}

/// The moment that every delivered status produces.
fn drew_status(activity: Activity) -> Moment {
    Moment::Status(Status {
        display_name: "Player One".into(),
        activity,
        ..Status::default()
    })
}

#[test]
fn the_screen_subscribes_to_every_topic_the_operator_set() {
    assert_eq!(
        Screen::new(&two_remotes()).filters(),
        [
            STATUS,
            VOLUME,
            COMMANDS,
            PANEL,
            POWER,
            SECOND_REMOTE_EVENTS,
            SECOND_REMOTE_FOCUS,
            FIRST_REMOTE_EVENTS,
            FIRST_REMOTE_FOCUS
        ]
    );
}

#[test]
fn a_player_with_no_volume_topic_subscribes_to_no_level() {
    let wiring = Wiring {
        volume_topic: String::new(),
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [
            STATUS,
            COMMANDS,
            PANEL,
            POWER,
            FIRST_REMOTE_EVENTS,
            FIRST_REMOTE_FOCUS
        ]
    );
}

#[test]
fn a_controller_with_no_focus_topic_adds_no_focus_subscription() {
    let wiring = Wiring {
        remotes: vec![Remote {
            events: FIRST_REMOTE_EVENTS.into(),
            focus: String::new(),
        }],
        ..wiring()
    };
    assert_eq!(
        Screen::new(&wiring).filters(),
        [STATUS, VOLUME, COMMANDS, PANEL, POWER, FIRST_REMOTE_EVENTS]
    );
}

#[test]
fn a_message_on_an_unsubscribed_topic_has_no_effect() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    assert!(
        screen
            .deliver("liken/media/plays/default/friday/status", b"{}", true, now)
            .is_empty(),
        "want no effects from a message on a topic the screen does not subscribe to"
    );
    assert!(
        screen.deliver("", b"{}", true, now).is_empty(),
        "want no effects from a message on an empty topic"
    );
}

// The client's own topics.

/// A topic that the client reads and `Screen` does not.
const WATCHING: &str = "liken/library/screens/default/player-1/watching";

#[test]
fn a_client_subscribes_to_its_own_topics_after_the_screens() {
    assert_eq!(
        Screen::new(&wiring()).reading(&[WATCHING.into()]).filters(),
        [
            STATUS,
            VOLUME,
            COMMANDS,
            PANEL,
            POWER,
            FIRST_REMOTE_EVENTS,
            FIRST_REMOTE_FOCUS,
            WATCHING
        ]
    );
}

#[test]
fn an_empty_client_topic_is_dropped() {
    assert_eq!(
        Screen::new(&wiring())
            .reading(&[String::new(), WATCHING.into()])
            .filters(),
        [
            STATUS,
            VOLUME,
            COMMANDS,
            PANEL,
            POWER,
            FIRST_REMOTE_EVENTS,
            FIRST_REMOTE_FOCUS,
            WATCHING
        ]
    );
}

#[test]
fn a_client_topic_is_subscribed_even_when_the_wiring_is_empty() {
    assert_eq!(
        Screen::new(&Wiring::default())
            .reading(&[WATCHING.into()])
            .filters(),
        [WATCHING]
    );
}

#[test]
fn every_message_on_a_client_topic_becomes_a_message_moment() {
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
fn a_client_topic_that_is_also_a_screen_topic_runs_only_the_screen_rule() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now).reading(&[STATUS.into()]);

    assert_eq!(
        moments(screen.deliver(STATUS, &status("Playing"), true, now)),
        [drew_status(Activity::Playing)]
    );
}

// The status.

#[test]
fn every_status_becomes_a_status_moment() {
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

    assert!(
        screen.deliver(STATUS, b"not json", true, now).is_empty(),
        "want no effects from a status that does not decode"
    );
    assert!(
        !screen.idle,
        "want the screen not idle after a status that does not decode"
    );
}

#[test]
fn a_status_that_leaves_idle_wakes_the_screen() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(STATUS, &status("Starting"), true, now)),
        [drew_status(Activity::Starting), Moment::Wake]
    );
}

#[test]
fn a_repeated_idle_status_does_not_restart_the_fade_timer() {
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

// Sleep.

#[test]
fn an_idle_player_sleeps_when_the_fade_timer_expires() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(
        screen.tick(now + Duration::from_secs(599)).is_empty(),
        "want no effects 1 second before the fade timer expires"
    );
    assert_eq!(
        moments(screen.tick(now + Duration::from_secs(600))),
        [Moment::Sleep]
    );
    assert!(
        screen.asleep,
        "want the screen asleep after the fade timer expires"
    );
}

#[test]
fn the_fade_timer_does_not_start_while_a_play_runs() {
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
fn a_fade_timer_of_zero_never_starts() {
    let now = Instant::now();
    let screen = idling(&wiring(), now);

    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_press_restarts_the_fade_timer() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    let later = now + Duration::from_secs(500);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, later);

    assert_eq!(
        screen.next_deadline(),
        Some(later + Duration::from_secs(600))
    );
}

#[test]
fn a_press_passed_to_the_client_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        publishes(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now))
            .is_empty(),
        "want no publishes from a press that is passed to the client"
    );
}

#[test]
fn an_event_that_does_not_decode_neither_wakes_the_screen_nor_restarts_the_fade_timer() {
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
            .deliver(FIRST_REMOTE_EVENTS, b"not json", false, now)
            .is_empty(),
        "want no effects from an event that does not decode"
    );
    assert!(
        screen.asleep,
        "want the screen asleep after an event that does not decode"
    );
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_release_neither_wakes_the_screen_nor_restarts_the_fade_timer() {
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
            .deliver(FIRST_REMOTE_EVENTS, &key("KEY_BACK", 0), false, later)
            .is_empty(),
        "want no effects from a key release"
    );
    assert!(screen.asleep, "want the screen asleep after a key release");
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn sleep_puts_the_screen_to_sleep_at_once() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(moments(screen.sleep(now)), [Moment::Sleep]);
    assert!(screen.asleep, "want the screen asleep after sleep");
}

#[test]
fn sleep_starts_the_off_timer() {
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
fn sleep_does_nothing_when_the_screen_already_sleeps() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert!(
        screen.sleep(now).is_empty(),
        "want no effects from sleep when the screen already sleeps"
    );
}

#[test]
fn sleep_does_nothing_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen.sleep(now).is_empty(),
        "want no effects from sleep while a play runs"
    );
}

// Keys.

#[test]
fn every_key_this_crate_does_not_handle_reaches_the_client() {
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
        "KEY_VOLUMEUP",
        "KEY_VOLUMEDOWN",
        "KEY_MUTE",
        "KEY_UNMUTE",
    ] {
        for value in [1, 2] {
            let mut screen = idling(&wiring(), now);
            assert_eq!(
                moments(screen.deliver(FIRST_REMOTE_EVENTS, &key(name, value), false, now)),
                [Moment::Press(name.into())],
                "{name} at value {value}"
            );
        }
    }
}

#[test]
fn the_cycle_key_does_not_reach_the_client() {
    let now = Instant::now();
    for value in [1, 2] {
        let mut screen = idling(&wiring(), now);
        assert!(
            moments(screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, value), false, now))
                .is_empty(),
            "value {value}"
        );
    }
}

#[test]
fn a_release_does_not_reach_the_client() {
    let now = Instant::now();
    for name in ["KEY_UP", "KEY_A", "KEY_HOMEPAGE", "KEY_BACKSPACE"] {
        let mut screen = idling(&wiring(), now);

        assert!(
            screen
                .deliver(FIRST_REMOTE_EVENTS, &key(name, 0), false, now)
                .is_empty(),
            "{name}"
        );
    }
}

#[test]
fn a_back_press_reaches_the_client_and_does_not_put_the_screen_to_sleep() {
    let now = Instant::now();
    for name in keys::BACK {
        let mut screen = idling(&wiring(), now);
        assert_eq!(
            moments(screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now)),
            [Moment::Press(name.into())]
        );
        assert!(!screen.asleep, "want the screen awake after a back press");
    }
}

/// The retained panel request that the screen publishes for
/// `media-operator`.
fn panel_request(desire: &'static str) -> Publish {
    Publish {
        topic: PANEL.into(),
        payload: crate::panel::Request { desire }.payload(),
        retained: true,
    }
}

#[test]
fn a_press_does_not_reach_the_client_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty(),
        "want no effects from a press while a play runs"
    );
}

#[test]
fn a_press_from_a_controller_focused_on_another_player_is_ignored() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(FIRST_REMOTE_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty(),
        "want no effects from a press by a controller focused on another Player"
    );
}

// Volume.

#[test]
fn a_retained_level_is_not_a_person_pressing_a_key() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME, br#"{"level":0.45,"muted":true}"#, true, now)),
        [Moment::Level {
            volume: Volume {
                level: 0.45,
                muted: true,
                ..Volume::default()
            },
            pressed: false,
        }]
    );
}

#[test]
fn a_live_level_is_a_change_a_person_made() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(VOLUME, br#"{"level":0.45,"muted":false}"#, false, now)),
        [Moment::Level {
            volume: Volume {
                level: 0.45,
                muted: false,
                ..Volume::default()
            },
            pressed: true,
        }]
    );
}

#[test]
fn a_level_that_does_not_decode_has_no_effect() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen.deliver(VOLUME, b"not json", false, now).is_empty(),
        "want no effects from a level that does not decode"
    );
}

#[test]
fn a_volume_key_does_not_publish_a_level() {
    let now = Instant::now();
    for name in ["KEY_VOLUMEUP", "KEY_VOLUMEDOWN", "KEY_MUTE", "KEY_UNMUTE"] {
        let mut screen = idling(&wiring(), now);
        assert!(
            publishes(screen.deliver(FIRST_REMOTE_EVENTS, &key(name, 1), false, now))
                .iter()
                .all(|publish| publish.topic != VOLUME),
            "{name}"
        );
    }
}

// Focus.

#[test]
fn a_live_focus_message_naming_this_player_wakes_the_screen_and_sends_a_focus_moment() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.focus[0].player = "cinema".into();
    screen.asleep = true;

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Wake, Moment::Focus { remote: 0 }]
    );
}

#[test]
fn the_retained_focus_message_neither_wakes_the_screen_nor_sends_a_focus_moment() {
    let now = Instant::now();
    let mut screen = Screen::new(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.asleep = true;

    assert!(
        screen
            .deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), true, now)
            .is_empty(),
        "want no effects from the retained focus message"
    );
    assert!(
        screen.asleep,
        "want the screen asleep after the retained focus message"
    );
}

#[test]
fn the_retained_focus_message_lets_presses_through() {
    let now = Instant::now();
    let mut screen = Screen::new(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), true, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now)),
        [Moment::Press("KEY_UP".into())]
    );
}

#[test]
fn a_repeated_focus_message_neither_wakes_the_screen_nor_sends_a_focus_moment() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    assert!(
        screen
            .deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)
            .is_empty(),
        "want no effects from a repeated focus message"
    );
    assert!(
        screen.asleep,
        "want the screen asleep after a repeated focus message"
    );
}

// When only one `Player` lists the controller, `media-operator` responds to a
// cycle request by publishing the same focus message again.
#[test]
fn a_repeated_focus_message_after_this_clients_cycle_request_wakes_the_screen_and_sends_a_focus_moment()
 {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Wake, Moment::Focus { remote: 0 }]
    );
    assert!(
        screen
            .deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)
            .is_empty(),
        "want no effects from a focus message repeated after the one that woke the screen"
    );
}

#[test]
fn a_focus_message_that_returns_to_this_player_sends_a_focus_moment() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(FIRST_REMOTE_FOCUS, b"cinema", false, now);

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
}

#[test]
fn the_focus_moment_has_the_controllers_index_in_spec_remotes() {
    let now = Instant::now();
    let mut screen = idling(&two_remotes(), now);
    for focus in &mut screen.focus {
        focus.player = "cinema".into();
    }

    assert_eq!(
        moments(screen.deliver(SECOND_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );
    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 1 }]
    );
}

#[test]
fn a_focus_message_naming_another_player_has_no_effect() {
    let now = Instant::now();
    for player in ["cinema", "friday-film", ""] {
        let mut screen = idling(&wiring(), now);
        assert!(
            screen
                .deliver(FIRST_REMOTE_FOCUS, player.as_bytes(), false, now)
                .is_empty(),
            "want no effects from a focus message that names another Player"
        );
    }
}

#[test]
fn a_client_with_no_player_name_matches_no_focus_message() {
    let wiring = Wiring {
        player_name: String::new(),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_FOCUS, b"", false, now)
            .is_empty(),
        "want no effects from a focus message when the client has no Player name"
    );
    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now)
            .is_empty(),
        "want no effects from a press when the client has no Player name"
    );
}

#[test]
fn a_new_bus_session_treats_the_next_focus_message_as_retained() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.focus[0].player = "cinema".into();
    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), false, now)),
        [Moment::Focus { remote: 0 }]
    );

    screen.connected();
    // This message moves the controller back to this `Player`. It sends no
    // focus moment only because it is the first message after the reconnect.
    screen.focus[0].player = "cinema".into();

    assert!(
        screen
            .deliver(FIRST_REMOTE_FOCUS, PLAYER.as_bytes(), true, now)
            .is_empty(),
        "want no effects from the first focus message after a reconnect"
    );
}

#[test]
fn the_focus_message_is_kept_across_a_bus_session() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.connected();

    assert_eq!(
        moments(screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_UP", 1), false, now)),
        [Moment::Press("KEY_UP".into())]
    );
}

// The cycle request.

#[test]
fn the_cycle_key_publishes_only_the_cycle_request() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert!(
        moments(effects.clone()).is_empty(),
        "want no moments from the cycle key"
    );
    assert_eq!(
        publishes(effects),
        [Publish {
            topic: format!("{FIRST_REMOTE_FOCUS}/cycle"),
            payload: Vec::new(),
            retained: false,
        }]
    );
}

#[test]
fn the_cycle_key_from_a_controller_focused_elsewhere_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.deliver(FIRST_REMOTE_FOCUS, b"cinema", true, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty(),
        "want no effects from the cycle key of a controller focused on another Player"
    );
}

#[test]
fn the_cycle_key_does_not_wake_the_screen() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now);

    assert!(
        moments(effects).is_empty(),
        "want no moments from the cycle key"
    );
    assert!(screen.asleep, "want the screen asleep after the cycle key");
}

#[test]
fn the_cycle_key_publishes_nothing_while_a_play_runs() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty(),
        "want no effects from the cycle key while a play runs"
    );
}

#[test]
fn a_controller_with_no_focus_topic_publishes_no_cycle() {
    let wiring = Wiring {
        remotes: vec![Remote {
            events: FIRST_REMOTE_EVENTS.into(),
            focus: String::new(),
        }],
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    // A controller with no focus topic never receives a focus message, so
    // the test sets one, or the press would be ignored before the cycle rule.
    screen.focus[0] = FocusMessage {
        player: PLAYER.into(),
        received_this_session: true,
        ..FocusMessage::default()
    };

    assert!(
        screen
            .deliver(FIRST_REMOTE_EVENTS, &key(keys::CYCLE, 1), false, now)
            .is_empty(),
        "want no effects from the cycle key of a controller with no focus topic"
    );
}

// The commands topic.

const PLAY_NEXT: &[u8] = br#"{"action":"play-next","request":{"library":"default/shows"}}"#;

#[test]
fn a_play_next_command_passes_its_request_whether_or_not_a_play_runs() {
    let now = Instant::now();
    let mut idle = idling(&wiring(), now);
    let mut playing = focused(&wiring());
    playing.deliver(STATUS, &status("Playing"), true, now);

    for screen in [&mut idle, &mut playing] {
        assert_eq!(
            moments(screen.deliver(COMMANDS, PLAY_NEXT, false, now)),
            [Moment::PlayNext(br#"{"library":"default/shows"}"#.to_vec())]
        );
    }
}

#[test]
fn a_play_next_command_with_no_request_passes_no_bytes() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert_eq!(
        moments(screen.deliver(COMMANDS, br#"{"action":"play-next"}"#, false, now)),
        [Moment::PlayNext(Vec::new())]
    );
}

const HOME_COMMAND: &[u8] = br#"{"action":"home"}"#;

#[test]
fn a_home_command_reaches_the_client_as_the_home_key_whether_or_not_a_play_runs() {
    let now = Instant::now();
    let mut idle = idling(&wiring(), now);
    let mut playing = focused(&wiring());
    playing.deliver(STATUS, &status("Playing"), true, now);

    for screen in [&mut idle, &mut playing] {
        assert_eq!(
            moments(screen.deliver(COMMANDS, HOME_COMMAND, false, now)),
            [Moment::Press(keys::HOME.into())]
        );
    }
}

#[test]
fn a_home_press_while_nothing_plays_reaches_the_client_and_publishes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key(keys::HOME, 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Press(keys::HOME.into())]);
    assert!(
        publishes(effects).is_empty(),
        "want no publishes from a home press"
    );
}

#[test]
fn any_other_action_and_a_payload_that_does_not_decode_have_no_effect() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    for payload in [
        &br#"{"action":"pause"}"#[..],
        &br#"{"action":"sleep"}"#[..],
        &b"not json"[..],
        &br#"["play-next"]"#[..],
        &br#"{}"#[..],
    ] {
        assert!(
            screen.deliver(COMMANDS, payload, false, now).is_empty(),
            "want no effects from this command payload"
        );
    }
}

#[test]
fn a_tick_before_the_deadline_or_with_no_deadline_has_no_effect() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = focused(&wiring);

    assert!(
        screen.tick(now).is_empty(),
        "want no effects from a tick with no deadline"
    );

    screen.deliver(STATUS, &status("Idle"), true, now);
    assert!(
        screen.tick(now + Duration::from_secs(1)).is_empty(),
        "want no effects from a tick before the deadline"
    );
}

mod lines;
mod panel;
mod power_keys;
mod power_topic;
mod waking_press;
