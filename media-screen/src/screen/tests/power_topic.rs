// The power topic. `media-operator` publishes `wake` or `sleep` on it when
// the `Player`'s `spec.power` changes, and when a TV requests this `Player`'s
// screen over HDMI-CEC. `wake` wakes the screen and requests the panel on,
// and `sleep` puts the screen to sleep and requests the panel off.

use super::*;

#[test]
fn every_player_subscribes_to_its_power_topic() {
    assert!(
        Screen::new(&wiring())
            .filters()
            .contains(&POWER.to_string()),
        "the filters do not include {POWER}"
    );
}

#[test]
fn a_wake_request_wakes_a_sleeping_screen_and_lights_the_panel() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.panel_request = Some(crate::panel::OFF);

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects), [panel_request(crate::panel::ON)]);
    assert!(
        !screen.asleep,
        "the screen is asleep after the wake request, want awake"
    );
    assert_eq!(
        screen.take_lines(),
        [format!(
            "{POWER} requested wake, so the screen is awake, published panel request on to {PANEL}"
        )]
    );
}

// The fade timer leaves the panel request at `on`, so there is nothing to
// publish.
#[test]
fn a_wake_request_wakes_the_screen() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert!(
        publishes(effects).is_empty(),
        "the wake request published a panel request, want none because the request is already on"
    );
}

// A client that has read no panel request cannot tell whether the panel is
// off.
#[test]
fn a_wake_request_publishes_on_when_no_panel_request_was_read() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.panel_request = None;

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert!(
        moments(effects.clone()).is_empty(),
        "the wake request produced a moment, want none because the screen is awake"
    );
    assert_eq!(publishes(effects), [panel_request(crate::panel::ON)]);
}

#[test]
fn a_wake_request_on_an_awake_screen_changes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"wake"}"#, false, now)
            .is_empty(),
        "the wake request on an awake screen produced effects, want none"
    );
    assert!(
        screen.take_lines().is_empty(),
        "the wake request on an awake screen wrote a log line, want none"
    );
}

#[test]
fn a_sleep_request_puts_the_screen_to_sleep_and_requests_the_panel_off() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(POWER, br#"{"action":"sleep"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Sleep]);
    assert_eq!(publishes(effects), [panel_request(crate::panel::OFF)]);
    assert!(
        screen.asleep,
        "the screen is awake after the sleep request, want asleep"
    );
    assert_eq!(screen.next_deadline(), None);
    assert_eq!(
        screen.take_lines(),
        [format!(
            "{POWER} requested sleep, so the screen is asleep, published panel request off to {PANEL}"
        )]
    );
}

#[test]
fn a_sleep_request_on_a_dark_screen_changes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.panel_request = Some(crate::panel::OFF);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"sleep"}"#, false, now)
            .is_empty(),
        "the sleep request on a dark screen produced effects, want none"
    );
    assert!(
        screen.take_lines().is_empty(),
        "the sleep request on a dark screen wrote a log line, want none"
    );
}

// Like the fade timer, `sleep` acts only while nothing plays.
#[test]
fn a_sleep_request_during_a_play_changes_nothing() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"sleep"}"#, false, now)
            .is_empty(),
        "the sleep request during a play produced effects, want none"
    );
    assert!(
        !screen.asleep,
        "the screen is asleep after a sleep request during a play, want awake"
    );
}

#[test]
fn every_other_action_and_a_payload_that_does_not_decode_change_nothing() {
    let now = Instant::now();
    for payload in [
        &br#"{"action":"toggle"}"#[..],
        br#"{"action":"on"}"#,
        br#"{"action":"off"}"#,
        b"not json",
        br#"["wake"]"#,
    ] {
        let mut screen = idling(&wiring(), now);
        screen.asleep = true;

        assert!(
            screen.deliver(POWER, payload, false, now).is_empty(),
            "the payload {:?} produced effects, want none",
            String::from_utf8_lossy(payload)
        );
    }
}
