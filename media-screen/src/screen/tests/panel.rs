// The panel request. A restarted client publishes no panel request until it
// reads the retained one back or an event changes it: the off timer expiring,
// a press, or a starting `Play`.

use super::*;

fn panel_request(word: &str) -> Vec<u8> {
    format!(r#"{{"desire":"{word}"}}"#).into_bytes()
}

fn published(word: &str) -> Publish {
    Publish {
        topic: PANEL.into(),
        payload: panel_request(word),
        retained: true,
    }
}

/// A client that just started and has not read a panel request yet.
fn restarted(wiring: &Wiring) -> Screen {
    let mut screen = focused(wiring);
    screen.panel_request = None;
    screen
}

/// A `Player` with both a fade timer and an off timer.
fn with_both_timers() -> Wiring {
    Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    }
}

#[test]
fn a_client_that_starts_publishes_no_panel_request() {
    let mut screen = restarted(&wiring());

    assert!(
        publishes(screen.connected()).is_empty(),
        "a client that starts published a panel request, want none"
    );
}

#[test]
fn a_restart_in_a_dark_room_keeps_the_room_dark() {
    let now = Instant::now();
    let mut screen = restarted(&with_both_timers());
    screen.connected();

    let adopted = screen.deliver(PANEL, &panel_request("off"), true, now);
    let idle = screen.deliver(STATUS, &status("Idle"), true, now);

    assert_eq!(moments(adopted.clone()), [Moment::Sleep]);
    assert!(
        publishes(adopted).is_empty(),
        "adopting the off request published a panel request, want none"
    );
    assert!(
        publishes(idle).is_empty(),
        "the Idle status in a dark room published a panel request, want none"
    );
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_press_in_an_adopted_dark_room_lights_it() {
    let now = Instant::now();
    let mut screen = restarted(&with_both_timers());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(PANEL, &panel_request("off"), true, now);

    let effects = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects), [published("on")]);
}

#[test]
fn a_play_that_starts_in_an_adopted_dark_room_lights_it() {
    let now = Instant::now();
    let mut screen = restarted(&with_both_timers());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(PANEL, &panel_request("off"), true, now);

    let effects = screen.deliver(STATUS, &status("Starting"), true, now);

    assert_eq!(publishes(effects), [published("on")]);
}

#[test]
fn a_restart_in_a_lit_room_publishes_nothing_until_the_off_timer() {
    let now = Instant::now();
    let mut screen = restarted(&with_both_timers());
    screen.connected();

    let adopted = screen.deliver(PANEL, &panel_request("on"), true, now);
    screen.deliver(STATUS, &status("Idle"), true, now);
    let pressed = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert!(
        adopted.is_empty(),
        "adopting the on request produced effects, want none"
    );
    assert!(
        publishes(pressed).is_empty(),
        "a press in an adopted lit room published a panel request, want none"
    );
    screen.tick(now + Duration::from_secs(600));
    assert_eq!(
        publishes(screen.tick(now + Duration::from_secs(1800))),
        [published("off")]
    );
}

#[test]
fn a_press_with_no_panel_request_on_the_broker_publishes_on() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);

    let first = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);
    screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 0), false, now);
    let second = screen.deliver(FIRST_REMOTE_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert_eq!(publishes(first), [published("on")]);
    assert!(
        publishes(second).is_empty(),
        "the second press published a panel request, want none because the client already requested on"
    );
}

#[test]
fn a_reconnect_publishes_the_clients_panel_request_again() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(PANEL, &panel_request("off"), true, now);

    assert_eq!(publishes(screen.connected()), [published("off")]);
}

#[test]
fn the_clients_panel_request_is_not_replaced_by_a_message_on_the_topic() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(PANEL, &panel_request("off"), true, now);

    for (payload, retained) in [
        (panel_request("on"), false),
        (panel_request("on"), true),
        (b"".to_vec(), true),
    ] {
        assert!(
            screen.deliver(PANEL, &payload, retained, now).is_empty(),
            "a message on the panel topic produced effects, want none"
        );
    }
    assert_eq!(screen.panel_request, Some(crate::panel::OFF));
}

#[test]
fn an_unknown_desire_value_is_ignored() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());

    assert!(
        screen
            .deliver(PANEL, &panel_request("dim"), true, now)
            .is_empty(),
        "the desire value dim produced effects, want none"
    );
    assert_eq!(screen.panel_request, None);
}

#[test]
fn a_player_with_no_panel_topic_publishes_no_panel_request() {
    let wiring = Wiring {
        panel_topic: String::new(),
        ..wiring()
    };
    let mut screen = Screen::new(&wiring);

    assert!(
        publishes(screen.connected()).is_empty(),
        "a player with no panel topic published a panel request, want none"
    );
}

#[test]
fn every_bus_session_tells_the_client_it_connected() {
    let mut screen = Screen::new(&wiring());

    assert_eq!(moments(screen.connected()), [Moment::Connected]);
}

#[test]
fn the_off_timer_requests_the_panel_off_behind_a_black_screen() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);

    let dark = now + Duration::from_secs(600);
    assert_eq!(moments(screen.tick(dark)), [Moment::Sleep]);
    // `off_after` counts from the same last activity as `fade_after`, not
    // from the moment the screen went to sleep.
    assert!(
        screen.tick(now + Duration::from_secs(1799)).is_empty(),
        "1799 s after the last activity the tick produced effects, want none before the 1800 s off timer"
    );
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
fn an_off_timer_of_zero_never_darkens_the_panel() {
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
fn a_press_requests_the_panel_on_and_relights_it() {
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
    let effects = screen.deliver(
        FIRST_REMOTE_EVENTS,
        &key("KEY_PLAYPAUSE", 1),
        false,
        pressed,
    );

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects)[0].payload, br#"{"desire":"on"}"#);
    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
}

#[test]
fn a_starting_play_requests_the_panel_on() {
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
fn a_press_before_the_off_timer_expires_keeps_the_panel_lit() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));

    let pressed = now + Duration::from_secs(1700);
    screen.deliver(
        FIRST_REMOTE_EVENTS,
        &key("KEY_PLAYPAUSE", 1),
        false,
        pressed,
    );

    // The press woke the screen, so the fade timer is armed again.
    assert_eq!(
        screen.next_deadline(),
        Some(pressed + Duration::from_secs(600))
    );
    assert!(
        screen.tick(now + Duration::from_secs(1800)).is_empty(),
        "the tick at 1800 s produced effects, want none because the press at 1700 s restarted the timers"
    );
    assert_eq!(screen.panel_request, Some(crate::panel::ON));
}

#[test]
fn a_dark_panel_arms_no_second_off_timer() {
    let wiring = Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    };
    let now = Instant::now();
    let mut screen = idling(&wiring, now);
    screen.tick(now + Duration::from_secs(600));
    screen.tick(now + Duration::from_secs(1800));
    assert_eq!(screen.panel_request, Some(crate::panel::OFF));

    // A press of the cycle key keeps the screen asleep and arms the timers
    // again, so it reaches the check that a dark panel needs no off timer.
    screen.deliver(
        FIRST_REMOTE_EVENTS,
        &key(keys::CYCLE, 1),
        false,
        now + Duration::from_secs(1900),
    );

    assert!(
        screen.asleep,
        "the screen woke on the cycle key, want it asleep"
    );
    assert_eq!(screen.next_deadline(), None);
}
