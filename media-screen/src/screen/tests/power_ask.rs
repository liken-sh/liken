// The power ask: the playback pod's command sidecar publishes it when a
// person presses power during a film, and this crate holds it until the
// unit's status reads `Idle`, then answers it the way it answers a power
// press between films.

use super::*;

/// The ask the sidecar publishes on a power press during a film.
const POWER_ASK: &[u8] = br#"{"action":"power"}"#;

/// A screen whose unit plays a film, the state in which a power ask arrives.
fn playing(wiring: &Wiring, now: Instant) -> Screen {
    let mut screen = focused(wiring);
    screen.deliver(STATUS, &status("Playing"), true, now);
    screen
}

#[test]
fn a_power_ask_during_a_play_publishes_nothing_and_draws_nothing() {
    let now = Instant::now();
    for wiring in [powered(), wiring()] {
        let mut screen = playing(&wiring, now);

        assert!(screen.deliver(COMMANDS, POWER_ASK, false, now).is_empty());
    }
}

#[test]
fn a_held_power_ask_publishes_the_toggle_when_the_unit_is_idle() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);

    let effects = screen.deliver(STATUS, &status("Idle"), false, now);

    assert_eq!(moments(effects.clone()), [drew_status(Activity::Idle)]);
    assert_eq!(publishes(effects), only_the_toggle());
}

#[test]
fn a_held_power_ask_reaches_the_client_as_a_power_press_with_no_receiver() {
    let now = Instant::now();
    let mut screen = playing(&wiring(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);

    let effects = screen.deliver(STATUS, &status("Idle"), false, now);

    assert_eq!(
        moments(effects.clone()),
        [
            drew_status(Activity::Idle),
            Moment::Press(keys::POWER_PRESS.into())
        ]
    );
    assert!(publishes(effects).is_empty());
}

// The operator republishes the status on any change to the payload, so a
// status that still reads a film is no ending, and the ask stays held.
#[test]
fn a_status_that_still_plays_leaves_the_ask_held() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);

    let effects = screen.deliver(STATUS, &status("Playing"), false, now);
    assert!(publishes(effects).is_empty());

    let effects = screen.deliver(STATUS, &status("Idle"), false, now);
    assert_eq!(publishes(effects), only_the_toggle());
}

#[test]
fn a_held_power_ask_is_answered_once() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);
    screen.deliver(STATUS, &status("Idle"), false, now);

    screen.deliver(STATUS, &status("Playing"), false, now);
    let effects = screen.deliver(STATUS, &status("Idle"), false, now);

    assert!(publishes(effects).is_empty());
}

#[test]
fn a_power_ask_while_the_unit_is_idle_is_answered_at_once() {
    let now = Instant::now();
    let mut screen = idling(&powered(), now);

    let effects = screen.deliver(COMMANDS, POWER_ASK, false, now);

    assert_eq!(publishes(effects), only_the_toggle());
}

#[test]
fn a_held_power_ask_arms_a_deadline_of_ten_seconds() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);

    screen.deliver(COMMANDS, POWER_ASK, false, now);

    assert_eq!(screen.next_deadline(), Some(now + Duration::from_secs(10)));
}

#[test]
fn a_held_power_ask_is_dropped_when_no_idle_status_arrives_by_the_deadline() {
    let now = Instant::now();
    let later = now + Duration::from_secs(10);
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);

    assert!(screen.tick(later).is_empty());
    assert_eq!(screen.next_deadline(), None);
    let effects = screen.deliver(STATUS, &status("Idle"), false, later);
    assert!(publishes(effects).is_empty());
}

#[test]
fn a_tick_before_the_deadline_keeps_the_ask_held() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);

    screen.tick(now + Duration::from_secs(9));
    let effects = screen.deliver(STATUS, &status("Idle"), false, now);

    assert_eq!(publishes(effects), only_the_toggle());
}

// The quiet window arms once the unit is idle, so the clock wakes at the
// earlier of the two deadlines.
#[test]
fn the_next_deadline_is_the_earlier_of_the_ask_and_the_quiet_window() {
    let now = Instant::now();
    let wiring = Wiring {
        fade_after: Duration::from_secs(5),
        ..powered()
    };
    let mut screen = idling(&wiring, now);
    screen.deliver(STATUS, &status("Playing"), false, now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);
    assert_eq!(screen.next_deadline(), Some(now + Duration::from_secs(10)));

    screen.deliver(COMMANDS, POWER_ASK, false, now);
    let effects = screen.deliver(STATUS, &status("Idle"), false, now);

    assert_eq!(publishes(effects), only_the_toggle());
    assert_eq!(screen.next_deadline(), Some(now + Duration::from_secs(5)));
}

#[test]
fn a_held_power_ask_writes_a_line_for_the_hold_and_for_the_answer() {
    let cases = [
        (
            powered(),
            format!(
                "{COMMANDS} asked for power while the player plays, held until it is Idle for at most 10 s"
            ),
            format!(
                "{COMMANDS} asked for power and the player is Idle, so published the toggle to {POWER}"
            ),
        ),
        (
            wiring(),
            format!(
                "{COMMANDS} asked for power while the player plays, held until it is Idle for at most 10 s"
            ),
            format!(
                "{COMMANDS} asked for power and the player is Idle, so passed to the client as KEY_POWER"
            ),
        ),
    ];
    for (wiring, held, answered) in cases {
        let now = Instant::now();
        let mut screen = playing(&wiring, now);

        screen.deliver(COMMANDS, POWER_ASK, false, now);
        assert_eq!(screen.take_lines(), [held]);

        screen.deliver(STATUS, &status("Idle"), false, now);
        assert_eq!(screen.take_lines(), [answered]);
    }
}

#[test]
fn a_dropped_power_ask_writes_one_line_that_says_so() {
    let now = Instant::now();
    let mut screen = playing(&powered(), now);
    screen.deliver(COMMANDS, POWER_ASK, false, now);
    screen.take_lines();

    screen.tick(now + Duration::from_secs(10));

    assert_eq!(
        screen.take_lines(),
        [format!(
            "{COMMANDS} asked for power, and no Idle status arrived within 10 s, so the ask is dropped"
        )]
    );
}
