// These tests run the idle screen's harness flags in a real window under cage,
// a wlroots compositor, on its headless backend. The window and the wgpu
// surface need a compositor, and a test run has no display. Under
// cargo-llvm-cov the binary is instrumented, so these runs count the frame
// loop and the graphics setup toward the coverage floor. A test fails when
// cage is missing, because a skip would let the run pass with coverage under
// the floor.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

// The binary that cargo built for this test run. Under coverage it is the
// instrumented one.
const BINARY: &str = env!("CARGO_BIN_EXE_idle-screen");

// The longest a run may take. A run still going after this is a hung
// compositor, and the test fails.
const CAP: Duration = Duration::from_secs(30);

struct Run {
    exit: String,
    log: String,
    seconds: f64,
    // The user plus system CPU time of the shell's child processes, which
    // is the client, from the shell's `times`.
    cpu: f64,
}

// A temporary directory for one run, so one run never reads another run's
// frames. Drop deletes it, because the temp directory can be a tmpfs, where
// the frames take memory. When the test panics the directory stays, so you
// can inspect the frames.
struct Workspace(PathBuf);

impl std::ops::Deref for Workspace {
    type Target = Path;

    fn deref(&self) -> &Path {
        &self.0
    }
}

impl Drop for Workspace {
    fn drop(&mut self) {
        if !std::thread::panicking() {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
}

fn workspace(name: &str) -> Workspace {
    let dir = std::env::temp_dir().join(format!("idle-screen-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).expect("creating the directory of the run");
    Workspace(dir)
}

// Runs the idle screen under cage with these flags and waits for it to end.
fn headless(dir: &Path, flags: &[&str]) -> Run {
    wired(dir, flags, &[])
}

// The same run, with the environment variables that the operator sets on the
// idle container.
fn wired(dir: &Path, flags: &[&str], wiring: &[(&str, String)]) -> Run {
    let log_path = dir.join("log");
    let exit_path = dir.join("exit");
    let log_file = std::fs::File::create(&log_path).expect("creating the log file");

    // The shell writes the client's exit status to a file, because cage
    // exits with the compositor's status.
    //
    // The headless output starts at 1280x720, and `wlr-randr` can change it
    // only after cage has started the command. So the shell prints the
    // `WAYLAND_DISPLAY` that cage set, waits for the `ready` file that the
    // test writes after it sets the mode, and only then starts the client. A
    // client that opened its window at once would sometimes draw and capture
    // at 1280x720.
    //
    // The wait checks every 50 ms for at most `CAP`, and then the shell exits
    // with status 1.
    let ready_path = dir.join("ready");
    let mut line = format!(
        "echo \"wayland: $WAYLAND_DISPLAY\"; waits=0; while [ ! -f {} ]; do \
         waits=$((waits+1)); if [ $waits -gt {} ]; then exit 1; fi; sleep 0.05; done; ",
        quoted(&text(&ready_path)),
        CAP.as_secs() * 20
    );
    line.push_str(&quoted(BINARY));
    for flag in flags {
        line.push(' ');
        line.push_str(&quoted(flag));
    }
    let times_path = dir.join("times");
    line.push_str(&format!(
        "; echo $? > {}; times > {}",
        quoted(&text(&exit_path)),
        quoted(&text(&times_path))
    ));

    let started = Instant::now();
    let child = Command::new("cage")
        .args(["--", "sh", "-c", &line])
        .envs(wiring.iter().map(|(name, value)| (*name, value.as_str())))
        .env_remove("WAYLAND_DISPLAY")
        .env("WLR_BACKENDS", "headless")
        .env("WLR_LIBINPUT_NO_DEVICES", "1")
        .stdout(Stdio::from(
            log_file
                .try_clone()
                .expect("duplicating the log file handle"),
        ))
        .stderr(Stdio::from(log_file))
        .spawn()
        .expect("starting cage with the headless backend");

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
        cpu: children_cpu(&read(&times_path)),
    }
}

// Owns cage and the shell under it. Every way out of a run, a panic
// included, drops this, which writes the `ready` file so the shell stops
// waiting, and kills and reaps the compositor.
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

// Sets the headless output to 1920x1080 once the shell prints the display
// name, and then writes `ready_path` to start the client. When cage exits
// before it prints a display name, this panics with cage's exit status and
// does not wait out `CAP`.
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
                .expect("running wlr-randr to set the headless mode");
            assert!(
                randr.status.success(),
                "wlr-randr on {display} exited unsuccessfully: {}",
                String::from_utf8_lossy(&randr.stderr)
            );
            std::fs::write(ready_path, "").expect("writing the file that releases the client");
            return;
        }
        if let Some(status) = child.try_wait().expect("checking whether cage has exited") {
            panic!(
                "cage exited with {status} before the client printed its display name\n{}",
                read(log_path)
            );
        }
        std::thread::sleep(Duration::from_millis(50));
    }

    panic!(
        "the client did not print its display name\n{}",
        read(log_path)
    );
}

// Waits for the run to end. At `CAP` it panics, and dropping `Cage` kills the
// run, so a hung compositor cannot hang the suite.
fn finish(child: &mut Child, log_path: &Path, started: Instant) {
    while started.elapsed() < CAP {
        if child
            .try_wait()
            .expect("checking whether cage has exited")
            .is_some()
        {
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }

    panic!(
        "the run was still going after {} s\n{}",
        CAP.as_secs(),
        read(log_path)
    );
}

// Asserts that a captured frame has a drawing in it. The elements are light
// on a black background, so a frame of one colour, or one with no bright
// pixel, means the client opened a window and drew nothing into it.
fn drawn(frame: &Path, run: &Run) {
    let pixels = image::open(frame)
        .unwrap_or_else(|error| {
            panic!(
                "reading the frame {}: {error}\n{}",
                frame.display(),
                run.log
            )
        })
        .to_rgb8();

    let ground = *pixels.get_pixel(0, 0);
    assert!(
        pixels.pixels().any(|pixel| *pixel != ground),
        "{} is all one colour, {ground:?}, want a drawing in it\n{}",
        frame.display(),
        run.log
    );

    let brightest = pixels
        .pixels()
        .flat_map(|pixel| pixel.0)
        .max()
        .expect("the frame has no pixels");
    assert!(
        brightest > 64,
        "the brightest value in {} is {brightest} of 255, want more than 64\n{}",
        frame.display(),
        run.log
    );
}

// Reads and parses the stats file that the run wrote.
fn measurements(path: &Path, run: &Run) -> serde_json::Value {
    let text = std::fs::read_to_string(path)
        .unwrap_or_else(|error| panic!("reading {}: {error}\n{}", path.display(), run.log));
    serde_json::from_str(&text)
        .unwrap_or_else(|error| panic!("parsing {}: {error}", path.display()))
}

// The shell's `times` prints two lines: its own user and system time, then
// its children's, each as `<m>m<s>s`. The second line is the client's.
fn children_cpu(times: &str) -> f64 {
    times
        .lines()
        .nth(1)
        .unwrap_or_default()
        .split_whitespace()
        .filter_map(|field| {
            let (minutes, seconds) = field.strip_suffix('s')?.split_once('m')?;
            Some(minutes.parse::<f64>().ok()? * 60.0 + seconds.parse::<f64>().ok()?)
        })
        .sum()
}

fn read(path: &Path) -> String {
    std::fs::read_to_string(path).unwrap_or_default()
}

fn text(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

// Quotes one argument for `sh -c`, which runs the client.
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

    assert_eq!(
        run.exit, "0",
        "the client exited with {}, want 0\n{}",
        run.exit, run.log
    );
    assert!(
        run.seconds < 20.0,
        "the run took {} s, want under 20 s because the q at 2.0 s should end it before the 25 s deadline\n{}",
        run.seconds,
        run.log
    );

    let measured = measurements(&stats, &run);
    assert!(
        measured["frames"].as_u64().unwrap_or(0) > 0,
        "the stats are {measured}, want a frame count above 0"
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

    assert_eq!(
        run.exit, "0",
        "the client exited with {}, want 0\n{}",
        run.exit, run.log
    );
    // A run that ends after its last capture takes the compositor's startup
    // plus a few seconds. A run that ends at the deadline takes the startup
    // plus the full 25 s. The bound is just under the deadline because the
    // compositor's startup is slow on a cold CI runner.
    assert!(
        run.seconds < 24.0,
        "the run took {} s, want under 24 s because the last capture should end it before the 25 s deadline\n{}",
        run.seconds,
        run.log
    );

    for name in ["002.00.png", "003.00.png"] {
        let frame = frames.join(name);
        assert_eq!(
            image::image_dimensions(&frame).ok(),
            Some((1920, 1080)),
            "the size of {} is not 1920x1080\n{}",
            frame.display(),
            run.log
        );
        drawn(&frame, &run);
    }

    let measured = measurements(&stats, &run);
    assert_eq!(measured["width"], serde_json::json!(1920));
    assert_eq!(measured["height"], serde_json::json!(1080));
    assert!(
        measured["frames"].as_u64().unwrap_or(0) > 0,
        "the stats are {measured}, want a frame count above 0"
    );
}

// The bus topics of one `Player`, in the form that `media-operator` builds
// with its default topic base.
const STATUS_TOPIC: &str = "liken/media/players/default/player-one/status";
const VOLUME_TOPIC: &str = "liken/media/players/default/player-one/volume";
const COMMANDS_TOPIC: &str = "liken/media/players/default/player-one/commands";

// A fake MQTT 3.1.1 broker for one client. It sends a CONNACK, reads the
// client's next packet, and then publishes every message, whatever the client
// subscribed to. It sends no SUBACK.
//
// The kernel picks the port, so two runs of this suite never collide.
fn broker(messages: Vec<Vec<u8>>) -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").expect("binding the broker to a port");
    let port = listener
        .local_addr()
        .expect("reading the broker port")
        .port();

    std::thread::spawn(move || {
        let (mut client, _) = listener.accept().expect("accepting the client connection");
        // Read the CONNECT and then the client's subscription packet, and
        // parse neither, because this test checks how the client decodes the
        // messages and not the broker.
        read_a_packet(&mut client);
        client
            .write_all(&[0x20, 0x02, 0x00, 0x00])
            .expect("writing the CONNACK");
        read_a_packet(&mut client);

        for message in messages {
            client.write_all(&message).expect("writing a PUBLISH");
            client.flush().expect("flushing a PUBLISH");
        }
        // The connection stays open for `CAP`, longer than any run, so the
        // client reads one session and never reconnects.
        std::thread::sleep(CAP);
    });

    port
}

// Reads one chunk of what the client writes and discards it. The 5 s read
// timeout keeps a client that writes nothing from blocking the broker's
// thread.
fn read_a_packet(client: &mut TcpStream) {
    client
        .set_read_timeout(Some(Duration::from_secs(5)))
        .expect("setting the read timeout");
    let _ = client.read(&mut [0_u8; 1024]);
}

// Encodes one QoS 0 PUBLISH. MQTT encodes a remaining length under 128 as one
// byte, and the assert enforces that limit.
fn publish(topic: &str, payload: &str) -> Vec<u8> {
    let length = 2 + topic.len() + payload.len();
    assert!(
        length < 128,
        "the remaining length is {length}, want under 128 because this broker writes one length byte"
    );

    let mut packet = vec![0x30, length as u8];
    packet.extend((topic.len() as u16).to_be_bytes());
    packet.extend(topic.as_bytes());
    packet.extend(payload.as_bytes());
    packet
}

// The unit tests in `media-screen` and in `unit` check what each payload
// means. This test checks the path that needs a broker and a compositor: the
// client connects, subscribes, reads the messages from the socket, and draws
// what they say.
//
// Every message below changes what the run draws. A panic while drawing any
// element ends the run with a nonzero exit status, which the test checks.
// The capture test above checks what the frames contain.
#[test]
fn the_client_reads_the_bus_and_draws_what_it_says() {
    let dir = workspace("bus");
    let stats = dir.join("stats.json");

    let port = broker(vec![
        // The unit's name and its activity, which the identity block draws.
        publish(
            STATUS_TOPIC,
            r#"{"displayName":"Player One","activity":"Idle"}"#,
        ),
        // The broker publishes these without the retained flag, so the client
        // reads both as live changes and draws the volume row. The row stays
        // 4 s and fades out over 600 ms, so it is gone before the 6 s quit.
        publish(VOLUME_TOPIC, r#"{"level":0.4,"muted":false}"#),
        publish(VOLUME_TOPIC, r#"{"level":0.45,"muted":false}"#),
        // A person took the up-next offer. The stock idle screen writes no
        // `Play`, so it ignores the message and the run keeps drawing.
        publish(COMMANDS_TOPIC, r#"{"action":"play-next"}"#),
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
            ("MEDIA_PLAYER_COMMANDS_TOPIC", COMMANDS_TOPIC.into()),
            ("IDLE_PLAYER_NAME", "Player One".into()),
            ("IDLE_PLAYER_COMPONENTS", "The screen\nThe speakers".into()),
            // Port 0 takes whatever the kernel has free, because this run
            // proves the client starts its listener and keeps drawing, not
            // that a scrape reaches one particular port.
            ("MEDIA_METRICS_ADDRESS", "127.0.0.1:0".into()),
            ("MEDIA_VERSION", "test".into()),
        ],
    );

    assert_eq!(
        run.exit, "0",
        "the client exited with {}, want 0\n{}",
        run.exit, run.log
    );

    let measured = measurements(&stats, &run);
    assert!(
        measured["frames"].as_u64().unwrap_or(0) > 0,
        "the stats are {measured}, want a frame count above 0"
    );
}

// A client whose window never draws sleeps through its grace period. The
// wgpu backend named here does not exist on Linux, so the window gets no
// surface and the watchdog starts counting. The loop sleeps until the grace
// period ends, and then the client exits with the watchdog's exit status. A
// loop that polled for the whole grace period would use about as much CPU
// time as the period is long.
#[test]
fn a_client_with_no_window_sleeps_through_its_grace() {
    let dir = workspace("grace");

    let run = wired(
        &dir,
        &["--size", "1920x1080", "--quit-after", "25"],
        &[
            ("IDLE_WINDOW_GRACE_SECONDS", "3".into()),
            ("WGPU_BACKEND", "metal".into()),
        ],
    );

    assert_eq!(
        run.exit, "7",
        "the client exited with {}, want 7\n{}",
        run.exit, run.log
    );
    assert!(
        run.cpu < 1.0,
        "the client used {} s of processor time in a 3 s grace period, want under 1 s\n{}",
        run.cpu,
        run.log
    );
}
