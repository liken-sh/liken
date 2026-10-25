// The deterministic power keys and the equipment operator's asks for the
// screen. A TV remote's Power Off Function and Power On Function (HDMI-CEC
// 1.3a, CEC 13.13.3) name the state they want, and a second press leaves
// the room in that state, so they publish off and on and not the toggle.
// The equipment operator publishes wake and sleep on the same power topic
// when the TV picks this unit's input or goes to standby.

use super::*;

// On a lit Player, the Power Off Function turns it off, and the Power On
// Function asks on for equipment that someone turned off another way.
#[test]
fn a_power_off_press_turns_a_lit_player_off_and_a_power_on_press_asks_on() {
    let now = Instant::now();
    let cases = [
        (keys::POWER_OFF, vec![Moment::Sleep], turned_off()),
        (keys::POWER_ON, Vec::new(), vec![power_ask("on")]),
    ];
    for (name, want_moments, want_publishes) in cases {
        let mut screen = idling(&powered(), now);

        let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

        assert_eq!(moments(effects.clone()), want_moments, "{name}");
        assert_eq!(publishes(effects), want_publishes, "{name}");
    }
}

#[test]
fn a_power_off_press_leaves_a_sleeping_unit_with_no_receiver_asleep() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::POWER_OFF, 1), false, now);

    assert!(effects.is_empty());
    assert!(screen.asleep);
}

#[test]
fn a_power_on_press_wakes_a_sleeping_unit_with_no_receiver() {
    let now = Instant::now();
    let mut screen = idling(&wiring(), now);
    screen.asleep = true;

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::POWER_ON, 1), false, now);

    assert_eq!(moments(effects), [Moment::Wake]);
    assert!(!screen.asleep);
}

#[test]
fn a_unit_with_a_receiver_subscribes_to_its_power_topic() {
    assert!(
        Screen::new(&powered())
            .filters()
            .contains(&POWER.to_string())
    );
    assert!(
        !Screen::new(&wiring())
            .filters()
            .contains(&POWER.to_string())
    );
}

#[test]
fn a_wake_ask_wakes_a_sleeping_screen_and_asks_for_the_panel() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);
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

#[test]
fn a_wake_ask_on_an_awake_screen_changes_nothing() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"wake"}"#, false, now)
            .is_empty()
    );
}

#[test]
fn a_sleep_ask_brings_the_shade_down_and_turns_the_panel_off() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);

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

// A film that plays goes on. The screen sleeps only while nothing plays,
// the same rule the quiet window follows.
#[test]
fn a_sleep_ask_during_a_play_changes_nothing() {
    let now = Instant::now();
    let mut screen = focused(&powered());
    screen.deliver(STATUS, &status("Playing"), true, now);

    assert!(
        screen
            .deliver(POWER, br#"{"action":"sleep"}"#, false, now)
            .is_empty()
    );
    assert!(!screen.asleep);
}

// The screen's own asks come back on the topic it publishes them on, and
// each one is the equipment operator's to answer.
#[test]
fn the_screens_own_power_ask_coming_back_changes_nothing() {
    let now = Instant::now();
    for action in ["toggle", "on", "off"] {
        let mut screen = idling(&powered(), now);
        screen.asleep = true;
        let payload = format!(r#"{{"action":"{action}"}}"#);

        assert!(
            screen
                .deliver(POWER, payload.as_bytes(), false, now)
                .is_empty(),
            "{action}"
        );
    }
}
