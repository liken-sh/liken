// The power topic. `media-operator` publishes wake and sleep there when the
// `Player`'s `spec.power` changes, and when a TV asks for this unit's screen
// over HDMI-CEC. The screen answers each the way a press or the quiet window
// would, whether or not the unit's screen is wired through a `Receiver`.

use super::*;

#[test]
fn every_unit_subscribes_to_its_power_topic() {
    assert!(
        Screen::new(&wiring())
            .filters()
            .contains(&POWER.to_string())
    );
}

#[test]
fn a_wake_ask_wakes_a_sleeping_screen_and_asks_for_the_panel() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.desire = Some(crate::panel::OFF);

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects), [desire(crate::panel::ON)]);
    assert!(!screen.asleep);
    assert_eq!(
        screen.take_lines(),
        [format!(
            "{POWER} asked to wake the screen, published panel desire on to {PANEL}"
        )]
    );
}

// The quiet window lowers the shade and leaves the desire on, so a wake
// behind the shade lifts it and states nothing new.
#[test]
fn a_wake_ask_behind_the_shade_lifts_it() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert!(publishes(effects).is_empty());
}

// A client that read no desire does not know whether the panel is dark,
// so a wake states the on desire.
#[test]
fn a_wake_ask_states_the_on_desire_a_client_has_not_read() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.desire = None;

    let effects = screen.deliver(POWER, br#"{"action":"wake"}"#, false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(publishes(effects), [desire(crate::panel::ON)]);
}

#[test]
fn a_wake_ask_on_an_awake_screen_changes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"wake"}"#, false, now)
            .is_empty()
    );
    assert!(screen.take_lines().is_empty());
}

#[test]
fn a_sleep_ask_brings_the_shade_down_and_turns_the_panel_off() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);

    let effects = screen.deliver(POWER, br#"{"action":"sleep"}"#, false, now);

    assert_eq!(moments(effects.clone()), [Moment::Sleep]);
    assert_eq!(publishes(effects), [desire(crate::panel::OFF)]);
    assert!(screen.asleep);
    assert_eq!(screen.next_deadline(), None);
    assert_eq!(
        screen.take_lines(),
        [format!(
            "{POWER} asked the screen to sleep, so the shade is down, published panel desire off to {PANEL}"
        )]
    );
}

#[test]
fn a_sleep_ask_on_a_dark_screen_changes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;
    screen.desire = Some(crate::panel::OFF);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"sleep"}"#, false, now)
            .is_empty()
    );
    assert!(screen.take_lines().is_empty());
}

// A film that plays goes on. The screen sleeps only while nothing plays,
// the same rule the quiet window follows.
#[test]
fn a_sleep_ask_during_a_play_changes_nothing() {
    let now = Instant::now();
    let mut screen = focused(&wiring());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"sleep"}"#, false, now)
            .is_empty()
    );
    assert!(!screen.asleep);
}

// Wake and sleep are the only words on the topic.
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

        assert!(screen.deliver(POWER, payload, false, now).is_empty());
    }
}
