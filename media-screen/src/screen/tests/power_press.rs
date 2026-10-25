// The power press of a unit whose screen is wired through a Receiver. The
// panel desire is the Player's power: a press that turns the Player off
// states off with the ask, and a press that turns it on wakes the screen,
// so the Receiver session's awake flag moves with the equipment.

use super::*;

/// An idle screen with a Receiver, its shade and desire set.
fn held(asleep: bool, desire: Option<&'static str>, now: Instant) -> Screen {
    let mut screen = idling(&powered(), now);
    screen.asleep = asleep;
    screen.desire = desire;
    screen
}

// The off ask goes out before the off desire, so the equipment operator
// reads the standby before the session's awake flag turns false.
#[test]
fn a_power_press_on_a_lit_player_asks_off_and_lowers_the_shade() {
    let now = Instant::now();
    for name in ["KEY_POWER", "KEY_POWER2", keys::POWER_OFF] {
        let mut screen = held(false, Some(crate::panel::ON), now);

        let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

        assert_eq!(moments(effects.clone()), [Moment::Sleep], "{name}");
        assert_eq!(
            publishes(effects),
            [power_ask("off"), desire(crate::panel::OFF)],
            "{name}"
        );
        assert!(screen.asleep, "{name}");
    }
}

// The quiet window lowers the shade and leaves the desire on, so the
// Player is still on behind a dark shade, and power turns it off.
#[test]
fn a_power_press_behind_the_shade_asks_off() {
    let now = Instant::now();
    let mut screen = held(true, Some(crate::panel::ON), now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(
        publishes(effects),
        [power_ask("off"), desire(crate::panel::OFF)]
    );
}

// The on desire turns the session awake, and that flip wakes the TV and
// the receiver. An on ask beside it would start a second wake.
#[test]
fn a_power_press_on_a_dark_player_wakes_the_screen_and_asks_nothing_more() {
    let now = Instant::now();
    for name in ["KEY_POWER", "KEY_POWER2", keys::POWER_ON] {
        let mut screen = held(true, Some(crate::panel::OFF), now);

        let effects = screen.deliver(SOFA_EVENTS, &key(name, 1), false, now);

        assert_eq!(moments(effects.clone()), [Moment::Wake], "{name}");
        assert_eq!(publishes(effects), [desire(crate::panel::ON)], "{name}");
        assert!(!screen.asleep, "{name}");
    }
}

// A lit Player whose equipment someone turned off flips no flag, so the
// Power On Function asks on.
#[test]
fn a_power_on_press_on_a_lit_player_asks_on() {
    let now = Instant::now();
    let mut screen = held(false, Some(crate::panel::ON), now);

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::POWER_ON, 1), false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(publishes(effects), [power_ask("on")]);
}

// A Power Off Function keeps a device in standby when repeated (HDMI-CEC
// 1.3a, CEC 13.13.3), so it asks off again and leaves the screen dark.
#[test]
fn a_power_off_press_on_a_dark_player_asks_off_again() {
    let now = Instant::now();
    let mut screen = held(true, Some(crate::panel::OFF), now);

    let effects = screen.deliver(SOFA_EVENTS, &key(keys::POWER_OFF, 1), false, now);

    assert!(moments(effects.clone()).is_empty());
    assert_eq!(publishes(effects), [power_ask("off")]);
    assert!(screen.asleep);
}

// A client that read no desire does not know whether the Player is on, so
// the toggle goes to the equipment operator, which reads the TV's power.
#[test]
fn a_power_press_with_no_desire_publishes_the_toggle() {
    let now = Instant::now();
    let mut screen = held(false, None, now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    assert_eq!(publishes(effects), [power_ask("toggle")]);
    assert_eq!(screen.desire, None);
}

#[test]
fn a_power_repeat_publishes_nothing() {
    let now = Instant::now();
    let mut screen = held(false, Some(crate::panel::ON), now);

    assert!(
        screen
            .deliver(SOFA_EVENTS, &key("KEY_POWER", 2), false, now)
            .is_empty()
    );
}

// The press after a power-off wakes the Player, the way any press wakes a
// dark screen.
#[test]
fn the_press_after_a_power_off_wakes_the_player() {
    let now = Instant::now();
    let mut screen = held(false, Some(crate::panel::ON), now);
    screen.deliver(SOFA_EVENTS, &key("KEY_POWER", 1), false, now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_HOMEPAGE", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects), [desire(crate::panel::ON)]);
}

// A power ask the playback pod held until Idle is answered as a press.
#[test]
fn a_held_power_ask_turns_a_lit_player_off() {
    let now = Instant::now();
    let mut screen = held(false, Some(crate::panel::ON), now);

    let effects = screen.deliver(COMMANDS, br#"{"action":"power"}"#, false, now);

    assert_eq!(
        publishes(effects),
        [power_ask("off"), desire(crate::panel::OFF)]
    );
    assert!(screen.asleep);
}
