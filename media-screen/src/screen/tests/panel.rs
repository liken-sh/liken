// The panel desire: the retained desire a client reads back before it
// states one, the desire a window or a person changes, and the rule that a
// restart states nothing.

use super::*;

/// The desire as it travels on the panel topic.
fn desire(word: &str) -> Vec<u8> {
    format!(r#"{{"desire":"{word}"}}"#).into_bytes()
}

/// The desire as a retained publish on the panel topic.
fn stated(word: &str) -> Publish {
    Publish {
        topic: PANEL.into(),
        payload: desire(word),
        retained: true,
    }
}

/// A client that just started: every mark caught up, and no panel desire
/// read yet.
fn restarted(wiring: &Wiring) -> Screen {
    let mut screen = focused(wiring);
    screen.desire = None;
    screen
}

/// A unit with both windows, so a test reads what each one states.
fn windowed() -> Wiring {
    Wiring {
        fade_after: Duration::from_secs(600),
        off_after: Duration::from_secs(1800),
        ..wiring()
    }
}

#[test]
fn a_client_that_starts_states_no_desire() {
    let mut screen = restarted(&wiring());

    assert!(publishes(screen.connected()).is_empty());
}

#[test]
fn a_restart_in_a_dark_room_keeps_the_room_dark() {
    let now = Instant::now();
    let mut screen = restarted(&windowed());
    screen.connected();

    let adopted = screen.deliver(PANEL, &desire("off"), true, now);
    let idle = screen.deliver(STATUS, &status("Idle"), true, now);

    assert_eq!(moments(adopted.clone()), [Moment::Sleep]);
    assert!(publishes(adopted).is_empty());
    assert!(publishes(idle).is_empty());
    assert_eq!(screen.next_deadline(), None);
}

#[test]
fn a_press_in_an_adopted_dark_room_lights_it() {
    let now = Instant::now();
    let mut screen = restarted(&windowed());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(PANEL, &desire("off"), true, now);

    let effects = screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert_eq!(moments(effects.clone()), [Moment::Wake]);
    assert_eq!(publishes(effects), [stated("on")]);
}

#[test]
fn a_play_that_starts_in_an_adopted_dark_room_lights_it() {
    let now = Instant::now();
    let mut screen = restarted(&windowed());
    screen.deliver(STATUS, &status("Idle"), true, now);
    screen.deliver(PANEL, &desire("off"), true, now);

    let effects = screen.deliver(STATUS, &status("Starting"), true, now);

    assert_eq!(publishes(effects), [stated("on")]);
}

#[test]
fn a_restart_in_a_lit_room_states_nothing_until_the_off_window() {
    let now = Instant::now();
    let mut screen = restarted(&windowed());
    screen.connected();

    let adopted = screen.deliver(PANEL, &desire("on"), true, now);
    screen.deliver(STATUS, &status("Idle"), true, now);
    let pressed = screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert!(adopted.is_empty());
    assert!(publishes(pressed).is_empty());
    screen.tick(now + Duration::from_secs(600));
    assert_eq!(
        publishes(screen.tick(now + Duration::from_secs(1800))),
        [stated("off")]
    );
}

#[test]
fn a_press_with_no_desire_on_the_broker_states_on() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(STATUS, &status("Idle"), true, now);

    let first = screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);
    screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 0), false, now);
    let second = screen.deliver(SOFA_EVENTS, &key("KEY_PLAYPAUSE", 1), false, now);

    assert_eq!(publishes(first), [stated("on")]);
    assert!(publishes(second).is_empty());
}

#[test]
fn a_reconnect_states_the_desire_the_client_holds_again() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(PANEL, &desire("off"), true, now);

    assert_eq!(publishes(screen.connected()), [stated("off")]);
}

#[test]
fn a_desire_the_client_holds_is_not_replaced_by_a_message_on_the_topic() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());
    screen.deliver(PANEL, &desire("off"), true, now);

    for (payload, retained) in [
        (desire("on"), false),
        (desire("on"), true),
        (b"".to_vec(), true),
    ] {
        assert!(screen.deliver(PANEL, &payload, retained, now).is_empty());
    }
    assert_eq!(screen.desire, Some(crate::panel::OFF));
}

#[test]
fn a_desire_this_client_does_not_state_is_no_desire() {
    let now = Instant::now();
    let mut screen = restarted(&wiring());

    assert!(screen.deliver(PANEL, &desire("dim"), true, now).is_empty());
    assert_eq!(screen.desire, None);
}

#[test]
fn a_player_with_no_panel_topic_states_no_desire() {
    let wiring = Wiring {
        panel_topic: String::new(),
        ..wiring()
    };
    let mut screen = Screen::new(&wiring);

    assert!(publishes(screen.connected()).is_empty());
}

#[test]
fn every_bus_session_tells_the_client_it_connected() {
    let mut screen = Screen::new(&wiring());

    assert_eq!(moments(screen.connected()), [Moment::Connected]);
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
    assert_eq!(screen.desire, Some(crate::panel::ON));
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
    assert_eq!(screen.desire, Some(crate::panel::OFF));

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
