// The harness flags, run against a real window under cage on the wlroots
// headless backend, the way local/media-browser --headless runs them. Under
// cargo-llvm-cov the binary is instrumented, so these runs count the frame
// loop and the graphics setup toward the coverage floor. The test fails when
// cage is missing; a skip would let a run pass under the floor.

use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

// The binary this test run built. Under coverage it is the instrumented one.
const BINARY: &str = env!("CARGO_BIN_EXE_media-browser");

// How long a run may take before the compositor counts as hung.
const CAP: Duration = Duration::from_secs(30);

// What one headless run left behind.
struct Run {
    exit: String,
    log: String,
    seconds: f64,
}

// A directory of this run's own, so one run never reads another's frames.
fn workspace(name: &str) -> PathBuf {
    let dir = std::env::temp_dir().join(format!("media-browser-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).expect("the run needs a directory of its own");
    dir
}

// Run the media browser under cage with these flags and wait for it to end.
fn headless(dir: &Path, flags: &[&str]) -> Run {
    headless_with(dir, &[], flags)
}

// The same run with variables set on the client alone. The app-id and the
// window grace arrive that way in a pod, so a test that reads them states
// them here and not on the command line.
fn headless_with(dir: &Path, environment: &[(&str, &str)], flags: &[&str]) -> Run {
    let log_path = dir.join("log");
    let exit_path = dir.join("exit");
    let log_file = std::fs::File::create(&log_path).expect("create the log");

    // The client's own status reaches the test through a file. cage stands
    // between the two, and the status cage returns is the compositor's.
    //
    // The client waits for a file this test writes once the output is at the
    // size the run expects. The headless output starts at 1280x720 and
    // `wlr-randr` cannot run until cage is up, which is after cage has already
    // started the client, so a client that opened its window at once would
    // race the mode and sometimes draw and capture at the smaller size.
    // The shell reports the display, not the client, because the client is
    // what waits: cage sets WAYLAND_DISPLAY for the command it runs, so the
    // name is known before anything opens a window.
    // The wait is counted rather than open, so a ready file that never
    // arrives ends the shell instead of spinning it for the life of the
    // machine. The count is `CAP` at one wake every 50 ms.
    let ready_path = dir.join("ready");
    let mut line = format!(
        "echo \"wayland: $WAYLAND_DISPLAY\"; waits=0; while [ ! -f {} ]; do \
         waits=$((waits+1)); if [ $waits -gt {} ]; then exit 1; fi; sleep 0.05; done; ",
        quoted(&text(&ready_path)),
        CAP.as_secs() * 20
    );
    for (name, value) in environment {
        line.push_str(&format!("{name}={} ", quoted(value)));
    }
    line.push_str(&quoted(BINARY));
    for flag in flags {
        line.push(' ');
        line.push_str(&quoted(flag));
    }
    line.push_str(&format!("; echo $? > {}", quoted(&text(&exit_path))));

    let started = Instant::now();
    let child = Command::new("cage")
        .args(["--", "sh", "-c", &line])
        .env_remove("WAYLAND_DISPLAY")
        .env("WLR_BACKENDS", "headless")
        .env("WLR_LIBINPUT_NO_DEVICES", "1")
        .stdout(Stdio::from(log_file.try_clone().expect("share the log")))
        .stderr(Stdio::from(log_file))
        .spawn()
        .expect("cage runs the client on the headless backend");

    let mut run = Cage {
        child,
        ready_path: &ready_path,
    };
    set_the_mode(&log_path, &ready_path, &mut run.child);
    finish(&mut run.child, &log_path, started);
    drop(run);

    Run {
        exit: read(&exit_path).trim().to_string(),
        seconds: started.elapsed().as_secs_f64(),
        log: read(&log_path),
    }
}

// Cage and the shell under it, held together so that every way out of
// this run releases the shell from its wait and reaps the compositor. A
// test that panics on an assertion drops this, and a shell left waiting
// on a file nothing writes would wake twenty times a second for as long
// as the machine stands.
struct Cage<'a> {
    child: Child,
    ready_path: &'a Path,
}

impl Drop for Cage<'_> {
    fn drop(&mut self) {
        let _ = std::fs::write(self.ready_path, "");
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

// The headless output starts at 1280x720, so the mode is set once the display
// is known. The client waits on `ready_path`, which this writes after the mode
// lands, so every frame it draws is at the size the run expects.
//
// A cage that ends before it names a display is reported as that, and
// not as the whole cap spent waiting for a line that can no longer
// arrive.
fn set_the_mode(log_path: &Path, ready_path: &Path, child: &mut Child) {
    let deadline = Instant::now() + CAP;
    while Instant::now() < deadline {
        if let Some(display) = read(log_path)
            .lines()
            .find_map(|line| line.strip_prefix("wayland: "))
        {
            let randr = Command::new("wlr-randr")
                .args(["--output", "HEADLESS-1", "--custom-mode", "1920x1080"])
                .env("WAYLAND_DISPLAY", display)
                .output()
                .expect("wlr-randr sets the headless mode");
            assert!(
                randr.status.success(),
                "wlr-randr on {display}: {}",
                String::from_utf8_lossy(&randr.stderr)
            );
            std::fs::write(ready_path, "").expect("release the client");
            return;
        }
        if let Some(status) = child.try_wait().expect("wait for cage") {
            panic!(
                "cage ended as {status} before it named a display\n{}",
                read(log_path)
            );
        }
        std::thread::sleep(Duration::from_millis(50));
    }

    panic!("the client never reported a display\n{}", read(log_path));
}

// Wait for the run, and kill it at the cap so a hung compositor never hangs the suite.
fn finish(child: &mut Child, log_path: &Path, started: Instant) {
    while started.elapsed() < CAP {
        if child.try_wait().expect("wait for cage").is_some() {
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }

    panic!(
        "the run did not end within {} s\n{}",
        CAP.as_secs(),
        read(log_path)
    );
}

// One captured frame carries a drawing. The elements are light on a
// black ground, so a frame of one colour, and a frame no brighter than
// that ground, are both a client that opened a window and drew nothing
// into it. The size alone proves neither.
fn drawn(frame: &Path, run: &Run) {
    let pixels = image::open(frame)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", frame.display(), run.log))
        .to_rgb8();

    let ground = *pixels.get_pixel(0, 0);
    assert!(
        pixels.pixels().any(|pixel| *pixel != ground),
        "{} is one colour, {ground:?}\n{}",
        frame.display(),
        run.log
    );

    let brightest = pixels
        .pixels()
        .flat_map(|pixel| pixel.0)
        .max()
        .expect("the frame has pixels");
    assert!(
        brightest > 64,
        "{} reaches {brightest} of 255, no brighter than its ground\n{}",
        frame.display(),
        run.log
    );
}

// The measurements the run wrote, parsed.
fn measurements(path: &Path, run: &Run) -> serde_json::Value {
    let text = std::fs::read_to_string(path)
        .unwrap_or_else(|error| panic!("{}: {error}\n{}", path.display(), run.log));
    serde_json::from_str(&text).unwrap_or_else(|error| panic!("{}: {error}", path.display()))
}

fn read(path: &Path) -> String {
    std::fs::read_to_string(path).unwrap_or_default()
}

fn text(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

// One argument as a shell word, because the client runs through sh.
fn quoted(argument: &str) -> String {
    format!("'{}'", argument.replace('\'', r"'\''"))
}

// A catalog fixture in the shape the sidecar's file has, with one
// library of one movie and a poster beside it on the volume.
fn fixture(dir: &Path) -> (PathBuf, PathBuf) {
    let database = dir.join("state.db");
    let connection = rusqlite::Connection::open(&database).expect("open the fixture database");
    let schema = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../corrosion/schema/catalog.sql"
    );
    connection
        .execute_batch(&std::fs::read_to_string(schema).expect("read the schema"))
        .expect("apply the schema");
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key, released, art) \
             VALUES ('drill/films', 'movie:path:one', 'movies', 'Vespera Coppice', \
             'vespera coppice', '1994', 'poster.jpg')",
            (),
        )
        .expect("insert the fixture movie");

    let volume = dir.join("volume");
    std::fs::create_dir_all(&volume).expect("the volume needs a directory");
    image::RgbImage::from_pixel(200, 300, image::Rgb([210, 140, 30]))
        .save(volume.join("poster.jpg"))
        .expect("write the fixture poster");

    (database, volume)
}

#[test]
fn a_catalog_run_draws_the_poster_the_volume_holds() {
    let dir = workspace("catalog");
    let frames = dir.join("frames");
    let (database, volume) = fixture(&dir);

    // No agent answers on port 1, so this run reads the file alone
    // and its update streams find nothing.
    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("drill/films={}", text(&volume)),
            "--script",
            "0.5:enter",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.00.png"), &run);
}

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
fn a_descent_draws_the_wall() {
    let dir = workspace("wall");
    let frames = dir.join("frames");

    let run = headless(
        &dir,
        &[
            "--script",
            "0.5:enter,0.8:right,1.1:down",
            "--capture",
            &text(&frames),
            "--capture-at",
            "1.6",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);

    let frame = frames.join("001.60.png");
    assert_eq!(
        image::image_dimensions(&frame).ok(),
        Some((1920, 1080)),
        "{}\n{}",
        frame.display(),
        run.log
    );
    drawn(&frame, &run);
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
