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
    let log_path = dir.join("log");
    let exit_path = dir.join("exit");
    let log_file = std::fs::File::create(&log_path).expect("create the log");

    // The client's own status reaches the test through a file. cage stands
    // between the two, and the status cage returns is the compositor's.
    let mut line = quoted(BINARY);
    for flag in flags {
        line.push(' ');
        line.push_str(&quoted(flag));
    }
    line.push_str(&format!("; echo $? > {}", quoted(&text(&exit_path))));

    let started = Instant::now();
    let mut child = Command::new("cage")
        .args(["--", "sh", "-c", &line])
        .env_remove("WAYLAND_DISPLAY")
        .env("WLR_BACKENDS", "headless")
        .env("WLR_LIBINPUT_NO_DEVICES", "1")
        .stdout(Stdio::from(log_file.try_clone().expect("share the log")))
        .stderr(Stdio::from(log_file))
        .spawn()
        .expect("cage runs the client on the headless backend");

    set_the_mode(&log_path, &mut child);
    finish(&mut child, &log_path, started);

    Run {
        exit: read(&exit_path).trim().to_string(),
        seconds: started.elapsed().as_secs_f64(),
        log: read(&log_path),
    }
}

// The headless output starts at 1280x720, so the mode is set once the client
// reports the display cage gave it.
fn set_the_mode(log_path: &Path, child: &mut Child) {
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
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }

    let _ = child.kill();
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

    let _ = child.kill();
    let _ = child.wait();
    panic!(
        "the run did not end within {} s\n{}",
        CAP.as_secs(),
        read(log_path)
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
    }

    let measured = measurements(&stats, &run);
    assert_eq!(measured["width"], serde_json::json!(1920));
    assert_eq!(measured["height"], serde_json::json!(1080));
    assert!(measured["frames"].as_u64().unwrap_or(0) > 0, "{measured}");
}
