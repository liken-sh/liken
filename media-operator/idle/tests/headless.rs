// The harness flags, run against a real window under cage on the wlroots
// headless backend, because the window and the wgpu surface need a compositor
// and a test run has no display. Under cargo-llvm-cov the binary is
// instrumented, so these runs count the frame loop and the graphics setup
// toward the coverage floor. The test fails when cage is missing; a skip would
// let a run pass under the floor.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

// The binary this test run built. Under coverage it is the instrumented one.
const BINARY: &str = env!("CARGO_BIN_EXE_idle-screen");

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
    let dir = std::env::temp_dir().join(format!("idle-screen-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).expect("the run needs a directory of its own");
    dir
}

// Run the idle screen under cage with these flags and wait for it to end.
fn headless(dir: &Path, flags: &[&str]) -> Run {
    wired(dir, flags, &[])
}

// The same run, with the variables the operator would set on the idle
// container.
fn wired(dir: &Path, flags: &[&str], wiring: &[(&str, String)]) -> Run {
    let log_path = dir.join("log");
    let exit_path = dir.join("exit");
    let log_file = std::fs::File::create(&log_path).expect("create the log");

    // The client's own status reaches the test through a file. cage stands
    // between the two, and the status cage returns is the compositor's.
    //
    // The client waits for a file this test writes once the output is at the
    // size the run expects. The headless output starts at 1280x720 and
    // `wlr-randr` cannot run until the display exists, which is after cage has
    // already started the client, so a client that opened its window at once
    // would race the mode and sometimes draw and capture at the smaller size.
    // The shell reports the display rather than the client, because the client
    // is what waits: cage sets WAYLAND_DISPLAY for the command it runs, so the
    // name is known before anything opens a window.
    let ready_path = dir.join("ready");
    let mut line = format!(
        "echo \"wayland: $WAYLAND_DISPLAY\"; while [ ! -f {} ]; do sleep 0.05; done; ",
        quoted(&text(&ready_path))
    );
    line.push_str(&quoted(BINARY));
    for flag in flags {
        line.push(' ');
        line.push_str(&quoted(flag));
    }
    line.push_str(&format!("; echo $? > {}", quoted(&text(&exit_path))));

    let started = Instant::now();
    let mut child = Command::new("cage")
        .args(["--", "sh", "-c", &line])
        .envs(wiring.iter().map(|(name, value)| (*name, value.as_str())))
        .env_remove("WAYLAND_DISPLAY")
        .env("WLR_BACKENDS", "headless")
        .env("WLR_LIBINPUT_NO_DEVICES", "1")
        .stdout(Stdio::from(log_file.try_clone().expect("share the log")))
        .stderr(Stdio::from(log_file))
        .spawn()
        .expect("cage runs the client on the headless backend");

    set_the_mode(&log_path, &ready_path, &mut child);
    finish(&mut child, &log_path, started);

    Run {
        exit: read(&exit_path).trim().to_string(),
        seconds: started.elapsed().as_secs_f64(),
        log: read(&log_path),
    }
}

// The headless output starts at 1280x720, so the mode is set once the display
// is known. The client waits on `ready_path`, which this writes after the mode
// lands, so every frame it draws is at the size the run expects.
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

// The topics one Player stands on, the way media-operator builds them.
const STATUS_TOPIC: &str = "media/players/den/tv/status";
const VOLUME_TOPIC: &str = "media/players/den/tv/volume";
const SCREEN_TOPIC: &str = "media/players/den/tv/screen";

// A broker for one client, speaking as much MQTT 3.1.1 as this test needs: it
// answers the CONNECT and then publishes. It acknowledges no subscription,
// because a broker decides what it delivers and this one delivers everything.
//
// The port is taken from the kernel, so two runs of this suite never collide.
fn broker(messages: Vec<Vec<u8>>) -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").expect("the broker takes a port");
    let port = listener.local_addr().expect("the port is known").port();

    std::thread::spawn(move || {
        let (mut client, _) = listener.accept().expect("the client connects");
        // The CONNECT, and then the subscriptions the client sends once it is
        // acknowledged. Both are read and neither is parsed, because the
        // client's own decoding is what this test measures.
        read_a_packet(&mut client);
        client
            .write_all(&[0x20, 0x02, 0x00, 0x00])
            .expect("connack");
        read_a_packet(&mut client);

        for message in messages {
            client.write_all(&message).expect("publish");
            client.flush().expect("publish");
        }
        // The connection stands until the client leaves, so the client reads
        // one session and never reconnects.
        std::thread::sleep(CAP);
    });

    port
}

// Whatever the client wrote, up to the read deadline. The deadline bounds the
// broker's own thread, so a client that writes nothing does not hold it.
fn read_a_packet(client: &mut TcpStream) {
    client
        .set_read_timeout(Some(Duration::from_secs(5)))
        .expect("the read is bounded");
    let _ = client.read(&mut [0_u8; 1024]);
}

// One QoS 0 PUBLISH. The topic and the payload are both short, so the
// remaining length is the one byte the protocol uses under 128.
fn publish(topic: &str, payload: &str) -> Vec<u8> {
    let length = 2 + topic.len() + payload.len();
    assert!(length < 128, "this broker writes one length byte");

    let mut packet = vec![0x30, length as u8];
    packet.extend((topic.len() as u16).to_be_bytes());
    packet.extend(topic.as_bytes());
    packet.extend(payload.as_bytes());
    packet
}

// What each payload means is a unit test in `bus` and in `unit`. What needs a
// broker and a compositor is the path between them: the client connects,
// subscribes, reads the messages off the socket, and draws what they say.
//
// Every message below reaches the frames this run draws, and the exit code is
// what proves they drew. A panic in an element ends the run with the code of
// the panic, and the screen this client draws is one canvas, so a frame that
// failed to draw fails this test.
#[test]
fn the_client_reads_the_bus_and_maps_a_new_surface_on_a_present() {
    let dir = workspace("bus");
    let stats = dir.join("stats.json");

    let port = broker(vec![
        // The unit's name and its activity, which the identity block draws.
        publish(
            STATUS_TOPIC,
            r#"{"displayName":"The Den","activity":"Idle"}"#,
        ),
        // The first level of a session is the broker's catch-up and shows no
        // indicator. The second is a press, and it is what lifts the volume
        // row: the row draws for four seconds, fades out over 600 ms, and is
        // gone well inside this run.
        publish(VOLUME_TOPIC, r#"{"level":40,"muted":false}"#),
        publish(VOLUME_TOPIC, r#"{"level":45,"muted":false}"#),
        // A `Play` ended, so the client maps a new Wayland surface.
        publish(SCREEN_TOPIC, r#"{"event":"present"}"#),
    ]);

    let run = wired(
        &dir,
        &[
            "--stats",
            &text(&stats),
            "--size",
            "1920x1080",
            "--quit-after",
            "6",
        ],
        &[
            ("MEDIA_BUS_ADDRESS", format!("127.0.0.1:{port}")),
            ("MEDIA_PLAYER_STATUS_TOPIC", STATUS_TOPIC.into()),
            ("MEDIA_PLAYER_VOLUME_TOPIC", VOLUME_TOPIC.into()),
            ("MEDIA_PLAYER_SCREEN_TOPIC", SCREEN_TOPIC.into()),
            ("IDLE_PLAYER_NAME", "The Den".into()),
            ("IDLE_PLAYER_COMPONENTS", "The screen\nThe speakers".into()),
            ("DISPLAY_APP_ID", "media-den-tv".into()),
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    assert!(
        run.log.contains("a new surface is up"),
        "the present mapped a new surface\n{}",
        run.log
    );

    // The frame loop kept drawing on the new surface.
    let measured = measurements(&stats, &run);
    assert!(measured["frames"].as_u64().unwrap_or(0) > 0, "{measured}");
}
