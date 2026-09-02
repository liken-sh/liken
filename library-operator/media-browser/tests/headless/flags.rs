// The flags a run takes: the key that ends it, the window it asks the
// compositor for, the frames it captures, and the bus it opens.

use super::*;

#[test]
fn the_scripted_quit_key_ends_the_run() {
    let dir = workspace("quit");
    let stats = dir.join("stats.json");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:down,2.0:q",
            "--stats",
            &text(&stats),
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    assert!(
        run.seconds < 20.0,
        "the q at 2.0 s ended the run, not the deadline at 25 s: {} s\n{}",
        run.seconds,
        run.log
    );

    let measured = measurements(&stats, &run);
    assert!(measured["frames"].as_u64().unwrap_or(0) > 0, "{measured}");
}

// A run whose claim named an app-id asks the compositor for a window
// under that name, and the armed watchdog stops when the window arrives:
// the run ends on its own key at 0, not at the watchdog's 7.
#[test]
fn a_claimed_screen_names_the_window_and_stops_the_watchdog() {
    let dir = workspace("app-id");

    let run = headless_with(
        &dir,
        &[
            ("DISPLAY_APP_ID", "media-den-tv"),
            ("WINDOW_GRACE_SECONDS", "15"),
        ],
        &[
            "--script",
            "0.5:q",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    assert!(
        run.seconds < 20.0,
        "the q at 0.5 s ended the run: {} s\n{}",
        run.seconds,
        run.log
    );
}

#[test]
fn a_capture_run_writes_its_frames_and_ends_after_the_last_one() {
    let dir = workspace("capture");
    let frames = dir.join("frames");
    let stats = dir.join("stats.json");

    let run = headless(
        &dir,
        &[
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.0,3.0",
            "--stats",
            &text(&stats),
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    assert!(
        run.seconds < 20.0,
        "the last capture ended the run, not the deadline at 25 s: {} s\n{}",
        run.seconds,
        run.log
    );

    for name in ["002.00.png", "003.00.png"] {
        let frame = frames.join(name);
        assert_eq!(
            image::image_dimensions(&frame).ok(),
            Some((1920, 1080)),
            "{}\n{}",
            frame.display(),
            run.log
        );
        drawn(&frame, &run);
    }

    let measured = measurements(&stats, &run);
    assert_eq!(measured["width"], serde_json::json!(1920));
    assert_eq!(measured["height"], serde_json::json!(1080));
    assert!(measured["frames"].as_u64().unwrap_or(0) > 0, "{measured}");
}

// A browser wired to a broker that answers nothing still draws the
// wall and takes its own keys. The variables are what the operator sets
// from the Player's status, and a broker that is down must not hold a
// screen dark.
#[test]
fn a_browser_wired_to_a_broker_that_is_down_still_draws() {
    let dir = workspace("bus");
    let frames = dir.join("frames");

    let run = headless_with(
        &dir,
        &[
            // A port on loopback that nothing listens on, so every
            // session the reader opens fails and it waits to try again.
            ("MEDIA_BUS_ADDRESS", "127.0.0.1:1"),
            ("MEDIA_PLAYER_NAME", "den-tv"),
            (
                "MEDIA_PLAYER_STATUS_TOPIC",
                "liken/media/players/house/den-tv/status",
            ),
            (
                "MEDIA_PLAYER_COMMANDS_TOPIC",
                "liken/media/players/house/den-tv/commands",
            ),
            (
                "MEDIA_PLAYER_PANEL_TOPIC",
                "liken/media/players/house/den-tv/panel",
            ),
            (
                "MEDIA_REMOTE_EVENTS_TOPICS",
                "liken/media/remotes/house/sofa/events",
            ),
            (
                "MEDIA_REMOTE_FOCUS_TOPICS",
                "liken/media/remotes/house/sofa/focus",
            ),
            ("IDLE_FADE_AFTER_SECONDS", "600"),
            ("IDLE_OFF_AFTER_SECONDS", "1800"),
            (
                "LIBRARY_PLAY_TOPIC",
                "liken/library/players/house/den-tv/play",
            ),
        ],
        &[
            "--script",
            "0.5:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.2",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("001.20.png"), &run);
}
