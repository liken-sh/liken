// The power mode the unit's status states. Every unit's client holds the
// power topic, and the status says where a power press goes: `room` publishes
// the ask on the topic, and `screen` hands the press to the client. The mode
// is read at the press and at the answer to a held power ask, so a `Receiver`
// that is wired or removed while the client runs moves the next press.

use super::*;

/// One status that states a power mode, the way the operator publishes it.
fn status_in(activity: &str, power: &str) -> Vec<u8> {
    format!(r#"{{"displayName":"The Theater","activity":"{activity}","power":"{power}"}}"#)
        .into_bytes()
}

/// A screen with the power topic, idle, whose last status stated the mode.
fn idling_in(power: &str, now: Instant) -> Screen {
    let mut screen = focused(&powered());
    screen.deliver(STATUS, &status_in("Idle", power), true, now);
    screen
}

/// What one power press hands the client, and what it publishes.
fn press_power(screen: &mut Screen, now: Instant) -> (Vec<Moment>, Vec<Publish>) {
    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);
    (moments(effects.clone()), publishes(effects))
}

/// A power press that reaches the client and publishes nothing.
fn to_the_client() -> (Vec<Moment>, Vec<Publish>) {
    (vec![Moment::Press("KEY_POWER".into())], Vec::new())
}

/// A power press that publishes the toggle and reaches no client.
fn to_the_room() -> (Vec<Moment>, Vec<Publish>) {
    (Vec::new(), only_the_toggle())
}

#[test]
fn the_mode_decides_where_a_power_press_goes() {
    let now = Instant::now();
    let cases = [
        ("room", to_the_room()),
        ("screen", to_the_client()),
        ("a word this client does not name", to_the_room()),
    ];
    for (power, want) in cases {
        let mut screen = idling_in(power, now);

        assert_eq!(press_power(&mut screen, now), want, "{power}");
    }
}

// An operator that predates the field states no mode, and it sets the power
// topic only for a unit with a `Receiver`, so the topic is the old rule.
#[test]
fn a_client_that_read_no_mode_follows_the_power_topic() {
    let now = Instant::now();
    let cases = [(powered(), to_the_room()), (wiring(), to_the_client())];
    for (wiring, want) in cases {
        let mut screen = idling(&wiring, now);

        assert_eq!(
            press_power(&mut screen, now),
            want,
            "{:?}",
            wiring.power_topic
        );
    }
}

// The room mode publishes on the power topic, so a client with no topic has
// nowhere to publish and keeps the press.
#[test]
fn the_room_mode_with_no_power_topic_hands_the_press_to_the_client() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status_in("Idle", "room"), true, now);

    assert_eq!(press_power(&mut screen, now), to_the_client());
}

// The operator republishes the status when the mode moves and the activity
// does not, so the mode is read before the status is compared for `Idle`.
#[test]
fn a_mode_that_moves_with_the_same_activity_moves_the_next_press() {
    let now = Instant::now();
    let cases = [
        ("room", "screen", to_the_client()),
        ("screen", "room", to_the_room()),
    ];
    for (before, after, want) in cases {
        let mut screen = idling_in(before, now);

        screen.deliver(STATUS, &status_in("Idle", after), false, now);

        assert_eq!(press_power(&mut screen, now), want, "{before} to {after}");
    }
}

// The operator states no mode for a unit it has not matched yet: after it
// starts, until its first pass reaches the unit, which can take seconds on a
// busy API server. A status with no mode keeps the last one.
#[test]
fn a_status_with_no_mode_keeps_the_last_mode() {
    let now = Instant::now();
    let mut screen = idling_in("screen", now);

    screen.deliver(STATUS, &status("Idle"), false, now);

    assert_eq!(press_power(&mut screen, now), to_the_client());
}

#[test]
fn a_held_power_ask_is_answered_in_the_mode_the_idle_status_states() {
    let now = Instant::now();
    let cases = [
        (
            "room",
            "screen",
            vec![Moment::Press(keys::POWER_PRESS.into())],
            Vec::new(),
        ),
        ("screen", "room", Vec::new(), only_the_toggle()),
    ];
    for (playing, idle, want_moments, want_publishes) in cases {
        let mut screen = focused(&powered());
        screen.deliver(STATUS, &status_in("Playing", playing), true, now);
        screen.deliver(COMMANDS, br#"{"action":"power"}"#, false, now);

        let effects = screen.deliver(STATUS, &status_in("Idle", idle), false, now);

        let drawn: Vec<Moment> = moments(effects.clone())
            .into_iter()
            .filter(|moment| !matches!(moment, Moment::Status(_)))
            .collect();
        assert_eq!(drawn, want_moments, "{playing} to {idle}");
        assert_eq!(publishes(effects), want_publishes, "{playing} to {idle}");
    }
}

#[test]
fn a_power_ask_while_idle_is_answered_in_the_screen_mode() {
    let now = Instant::now();
    let mut screen = idling_in("screen", now);

    let effects = screen.deliver(COMMANDS, br#"{"action":"power"}"#, false, now);

    assert_eq!(
        moments(effects.clone()),
        [Moment::Press(keys::POWER_PRESS.into())]
    );
    assert!(publishes(effects).is_empty());
}

// A unit with no `Receiver` handles power on the screen, so a late wake or
// sleep for its screen is not the screen's to act on.
#[test]
fn the_screen_mode_ignores_wake_and_sleep() {
    let now = Instant::now();
    for (action, asleep) in [("wake", true), ("sleep", false)] {
        let mut screen = idling_in("screen", now);
        screen.asleep = asleep;
        let payload = format!(r#"{{"action":"{action}"}}"#);

        let effects = screen.deliver(POWER, payload.as_bytes(), false, now);

        assert!(effects.is_empty(), "{action}");
        assert_eq!(screen.asleep, asleep, "{action}");
        assert_eq!(
            screen.take_lines(),
            [format!(
                "{POWER} asked the screen to {action}, ignored, because the power mode is screen"
            )],
            "{action}"
        );
    }
}
