// Restarts the client inside its container when the compositor restarts.
//
// When the compositor restarts, the client's window is gone and the process
// cannot reconnect, so the window watchdog exits with `NO_WINDOW`. The display
// operator restarts the compositor inside the compositor's own container. If
// the client's exit also ended the client's container, the kubelet would count
// a crash, and a second restart within ten minutes would leave the screen dark
// for the kubelet's backoff: 10 seconds, then 20, doubling up to 5 minutes.
//
// So in a pod the container's first process runs the client as a child. When
// the child exits with `NO_WINDOW` and a new compositor has replaced the one
// that was running when the child started, the first process waits until the
// new compositor's socket accepts a connection and starts the child again.
// Each compositor binds a new socket file at the same path, and `Identity`
// tells the two files apart.
//
// The container exits in the other cases:
// - The child exits with `NO_WINDOW`, and the compositor is the same one or
//   no compositor answered when the child started. A new child would get the
//   same result.
// - The child exits with any other status. The container exits with that
//   status, so the kubelet's backoff still limits a crash loop.
// - No compositor accepts a connection within `COMPOSITOR_RETURN_LIMIT`. The
//   container exits with `NO_WINDOW`, and the kubelet's restarts do the
//   waiting.
//
// `IDLE_WINDOW_GRACE_SECONDS` turns this on, as it turns on the watchdog. The
// operator sets it on the idle container only, so a run by hand runs the
// client directly.

use std::os::unix::fs::MetadataExt;
use std::os::unix::net::UnixStream;
use std::os::unix::process::ExitStatusExt;
use std::path::PathBuf;
use std::process::{Child, Command, ExitStatus};
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::time::{Duration, Instant};

use super::watchdog::NO_WINDOW;

/// Set in the child's environment, so the child runs the client instead of
/// supervising a second child.
const CHILD_VARIABLE: &str = "LIKEN_SCREEN_CHILD";

/// How long the first process waits for a compositor before it exits. A
/// compositor container in the kubelet's crash backoff can wait up to 5
/// minutes for its restart, so the limit is longer than that.
const COMPOSITOR_RETURN_LIMIT: Duration = Duration::from_secs(6 * 60);

/// The first and the longest pause between connection attempts while the
/// compositor is down. The pause doubles after each attempt. A down
/// compositor sends no event that a client can wait on, so each attempt is a
/// connect.
const FIRST_PAUSE: Duration = Duration::from_millis(100);
const LONGEST_PAUSE: Duration = Duration::from_secs(2);

/// Runs the client as a child process when the window grace is armed and this
/// process is not already the child. Returns the status for the container to
/// exit with, or `None` when this process should run the client itself.
pub fn supervise(armed: bool) -> Option<i32> {
    if !armed || std::env::var_os(CHILD_VARIABLE).is_some() {
        return None;
    }
    let program = match std::env::current_exe() {
        Ok(program) => program,
        Err(error) => {
            eprintln!(
                "idle-screen: reading the path of this program to run the client as a child process: {error}"
            );
            return None;
        }
    };
    forward_stops();
    let arguments: Vec<_> = std::env::args_os().skip(1).collect();
    let socket = compositor_socket();
    let status = respawn(
        || {
            Command::new(&program)
                .args(&arguments)
                .env(CHILD_VARIABLE, "1")
                .spawn()
        },
        || await_compositor(socket.as_ref(), COMPOSITOR_RETURN_LIMIT),
        || compositor_identity(socket.as_ref()),
    );
    Some(status)
}

/// The loop of [`supervise`]. Returns the status for the container to exit
/// with. The steps are parameters, so that tests can run the loop with real
/// child processes and no socket.
pub fn respawn(
    mut start: impl FnMut() -> std::io::Result<Child>,
    mut compositor_answers: impl FnMut() -> bool,
    mut compositor: impl FnMut() -> Option<Identity>,
) -> i32 {
    loop {
        if !compositor_answers() {
            eprintln!(
                "idle-screen: the compositor did not respond within {}s, so the container exits with status {NO_WINDOW} and the kubelet restarts it",
                COMPOSITOR_RETURN_LIMIT.as_secs()
            );
            return NO_WINDOW;
        }
        let compositor_at_start = compositor();
        let mut child = match start() {
            Ok(child) => child,
            Err(error) => {
                eprintln!("idle-screen: starting the client process: {error}");
                return 1;
            }
        };
        CHILD.store(child.id() as i32, Ordering::SeqCst);
        let status = child.wait();
        CHILD.store(0, Ordering::SeqCst);
        let code = match status {
            Ok(status) => exit_code(status),
            Err(error) => {
                eprintln!("idle-screen: waiting for the client process: {error}");
                return 1;
            }
        };
        if code != NO_WINDOW || STOPPING.load(Ordering::SeqCst) {
            return code;
        }
        if compositor_at_start.is_none() || compositor() == compositor_at_start {
            eprintln!(
                "idle-screen: the compositor is running and did not give the client a window, so the container exits with status {NO_WINDOW} and the kubelet restarts it"
            );
            return NO_WINDOW;
        }
        eprintln!(
            "idle-screen: the client lost its window, and the supervisor starts it again when the compositor responds"
        );
    }
}

/// The status that a shell reports: the exit code, or 128 plus the number of
/// the signal that ended the child.
fn exit_code(status: ExitStatus) -> i32 {
    match (status.code(), status.signal()) {
        (Some(code), _) => code,
        (None, Some(signal)) => 128 + signal,
        (None, None) => 1,
    }
}

/// The path of the compositor's socket, found as a Wayland client finds it:
/// `WAYLAND_DISPLAY` when it is an absolute path, otherwise that name inside
/// `XDG_RUNTIME_DIR`. Returns `None` when the variables it needs are not set.
fn compositor_socket() -> Option<PathBuf> {
    let display = PathBuf::from(std::env::var_os("WAYLAND_DISPLAY")?);
    if display.is_absolute() {
        return Some(display);
    }
    Some(PathBuf::from(std::env::var_os("XDG_RUNTIME_DIR")?).join(display))
}

/// Identifies one compositor by the device, inode, and change time (seconds
/// and nanoseconds) of the socket it bound. A new socket can reuse the inode
/// of the one it replaced, so the change time is what tells them apart.
pub type Identity = (u64, u64, i64, i64);

/// The identity of the compositor that accepts connections on the socket now,
/// or `None` when none does or the container has no socket path.
fn compositor_identity(socket: Option<&PathBuf>) -> Option<Identity> {
    let socket = socket?;
    UnixStream::connect(socket).ok()?;
    let metadata = std::fs::metadata(socket).ok()?;
    Some((
        metadata.dev(),
        metadata.ino(),
        metadata.ctime(),
        metadata.ctime_nsec(),
    ))
}

/// Waits up to `limit` for the compositor to accept a connection, and returns
/// whether it did. With no socket path it returns true, and the child's own
/// connect decides.
fn await_compositor(socket: Option<&PathBuf>, limit: Duration) -> bool {
    let Some(socket) = socket else {
        return true;
    };
    let deadline = Instant::now() + limit;
    let mut pause = FIRST_PAUSE;
    while UnixStream::connect(socket).is_err() {
        if Instant::now() >= deadline {
            return false;
        }
        std::thread::sleep(pause);
        pause = (pause * 2).min(LONGEST_PAUSE);
    }
    true
}

/// The running child's pid, or 0, and whether a stop signal has arrived. The
/// signal handler reads both, so they are atomics.
static CHILD: AtomicI32 = AtomicI32::new(0);
static STOPPING: AtomicBool = AtomicBool::new(false);

/// Forwards SIGTERM (from the kubelet) and SIGINT to the child, and records
/// that a stop was requested so the loop does not start the child again. The
/// first process of a container is pid 1 in the container's process
/// namespace, and the kernel does not deliver a signal to pid 1 unless it has
/// a handler. Without this handler, a stop would wait out the pod's
/// termination grace period.
fn forward_stops() {
    extern "C" fn pass(signal: libc::c_int) {
        STOPPING.store(true, Ordering::SeqCst);
        let child = CHILD.load(Ordering::SeqCst);
        if child > 0 {
            // SAFETY: kill is async-signal-safe.
            unsafe {
                libc::kill(child, signal);
            }
        }
    }
    for signal in [libc::SIGTERM, libc::SIGINT] {
        // SAFETY: the handler touches only atomics and calls only kill.
        unsafe {
            let mut action: libc::sigaction = std::mem::zeroed();
            action.sa_sigaction = pass as extern "C" fn(libc::c_int) as usize;
            action.sa_flags = libc::SA_RESTART;
            libc::sigaction(signal, &action, std::ptr::null_mut());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A child process that exits with `code`.
    fn exits(code: i32) -> std::io::Result<Child> {
        Command::new("sh")
            .args(["-c", &format!("exit {code}")])
            .spawn()
    }

    /// A reader that returns each given identity in turn, then `None`, so a
    /// test scripts each compositor restart.
    fn compositors(identities: Vec<Option<Identity>>) -> impl FnMut() -> Option<Identity> {
        let mut identities = identities.into_iter();
        move || identities.next().flatten()
    }

    #[test]
    fn a_child_that_lost_its_window_to_a_new_compositor_is_started_again() {
        let mut codes = vec![NO_WINDOW, NO_WINDOW, 0].into_iter();
        let mut starts = 0;

        // A new compositor replaces the old one while each child runs.
        let status = respawn(
            || {
                starts += 1;
                exits(codes.next().unwrap())
            },
            || true,
            compositors(vec![
                Some((1, 1, 0, 0)),
                Some((1, 2, 0, 0)),
                Some((1, 2, 0, 0)),
                Some((1, 3, 0, 0)),
                Some((1, 3, 0, 0)),
            ]),
        );

        assert_eq!(status, 0);
        assert_eq!(starts, 3);
    }

    #[test]
    fn a_child_that_lost_its_window_while_no_compositor_answers_starts_again() {
        let mut codes = vec![NO_WINDOW, 0].into_iter();
        let mut starts = 0;

        let status = respawn(
            || {
                starts += 1;
                exits(codes.next().unwrap())
            },
            || true,
            compositors(vec![Some((1, 1, 0, 0)), None, Some((1, 2, 0, 0))]),
        );

        assert_eq!(status, 0);
        assert_eq!(starts, 2);
    }

    #[test]
    fn a_child_that_got_no_window_from_a_running_compositor_ends_the_container() {
        let mut starts = 0;

        let status = respawn(
            || {
                starts += 1;
                exits(NO_WINDOW)
            },
            || true,
            compositors(vec![Some((1, 1, 0, 0)), Some((1, 1, 0, 0))]),
        );

        assert_eq!(status, NO_WINDOW);
        assert_eq!(starts, 1);
    }

    #[test]
    fn a_container_with_no_socket_path_exits_with_no_window_when_the_child_does() {
        let mut starts = 0;

        let status = respawn(
            || {
                starts += 1;
                exits(NO_WINDOW)
            },
            || true,
            || None,
        );

        assert_eq!(status, NO_WINDOW);
        assert_eq!(starts, 1);
    }

    #[test]
    fn a_crash_ends_the_container_with_its_status() {
        let mut starts = 0;

        let status = respawn(
            || {
                starts += 1;
                exits(3)
            },
            || true,
            || Some((1, 1, 0, 0)),
        );

        assert_eq!(status, 3);
        assert_eq!(starts, 1);
    }

    #[test]
    fn a_compositor_that_never_returns_exits_with_no_window_and_starts_no_child() {
        let mut starts = 0;

        let status = respawn(
            || {
                starts += 1;
                exits(0)
            },
            || false,
            || None,
        );

        assert_eq!(status, NO_WINDOW);
        assert_eq!(starts, 0);
    }

    #[test]
    fn a_child_ended_by_a_signal_reports_it_as_a_shell_does() {
        let status = respawn(
            || Command::new("sh").args(["-c", "kill -9 $$"]).spawn(),
            || true,
            || Some((1, 1, 0, 0)),
        );

        assert_eq!(status, 128 + libc::SIGKILL);
    }

    #[test]
    fn the_wait_ends_when_the_socket_accepts() {
        let directory = std::env::temp_dir().join(format!("respawn-{}", std::process::id()));
        std::fs::create_dir_all(&directory).unwrap();
        let socket = directory.join("wayland-1");
        let _ = std::fs::remove_file(&socket);
        let _listener = std::os::unix::net::UnixListener::bind(&socket).unwrap();

        assert!(await_compositor(Some(&socket), Duration::ZERO));
        assert!(compositor_identity(Some(&socket)).is_some());
        assert_eq!(
            compositor_identity(Some(&directory.join("wayland-0"))),
            None
        );
        assert!(await_compositor(None, Duration::ZERO));
        assert!(!await_compositor(
            Some(&directory.join("wayland-0")),
            Duration::ZERO
        ));
        let _ = std::fs::remove_dir_all(&directory);
    }

    #[test]
    fn a_run_by_hand_runs_the_client_in_place() {
        assert_eq!(supervise(false), None);
    }
}
