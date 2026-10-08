// Starting the client again inside its container when its compositor
// restarts.
//
// A compositor restart takes the client's window, and nothing inside the
// process can open the connection again, so the window watchdog ends the
// process with `NO_WINDOW`. The display operator restarts its compositor
// inside the compositor's container, so the compositor is back within a
// second or two. If the client's exit ended its container, the kubelet would
// count the exit as a crash, and a second restart within ten minutes would
// leave the screen dark for 10 seconds, then 20, and so on up to 5 minutes.
//
// So in a pod, the container's first process runs the client as its child.
// When the child exits with `NO_WINDOW` because a new compositor replaced the
// one that gave its window, the first process waits for the new compositor's
// socket to accept a connection and starts the child again. Each compositor
// binds the socket again, so a new compositor is a new file at the socket's
// path. A child that got no window from a compositor that still runs ends
// the container with `NO_WINDOW`, because starting it again would get the
// same answer. Any other exit ends the container with the child's status, so the kubelet's
// backoff still bounds a crash loop, and the container's restart count is
// the count of crashes. A compositor that does not come back within
// `COMPOSITOR_RETURN_LIMIT` also ends the container, with `NO_WINDOW`, and
// the kubelet takes over the wait.
//
// The window grace arms this, as it arms the watchdog: the operator sets it
// on the browser container of every screen pod, and a run by hand sets nothing,
// so it runs the client in place.

use std::os::unix::fs::MetadataExt;
use std::os::unix::net::UnixStream;
use std::os::unix::process::ExitStatusExt;
use std::path::PathBuf;
use std::process::{Child, Command, ExitStatus};
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::time::{Duration, Instant};

use super::watchdog::NO_WINDOW;

/// The variable that marks the child, so the child runs the client and not a
/// second first process.
const CHILD_VARIABLE: &str = "LIKEN_SCREEN_CHILD";

/// How long the first process waits for the compositor to answer before it
/// hands the wait to the kubelet. A compositor that crashed waits in the
/// kubelet's crash backoff, which doubles up to 5 minutes, so the limit is
/// past that cap.
const COMPOSITOR_RETURN_LIMIT: Duration = Duration::from_secs(6 * 60);

/// The first and the longest pause between two looks at the compositor's
/// socket while it does not answer. The compositor writes no event a client
/// can wait on while it is down, so each look is one connect.
const FIRST_PAUSE: Duration = Duration::from_millis(100);
const LONGEST_PAUSE: Duration = Duration::from_secs(2);

/// Runs the client as a child of this process when the window grace is armed
/// and this process is not the child already. It returns the status the
/// container exits with, or nothing when this process should run the client
/// itself.
pub fn supervise(armed: bool) -> Option<i32> {
    if !armed || std::env::var_os(CHILD_VARIABLE).is_some() {
        return None;
    }
    let program = match std::env::current_exe() {
        Ok(program) => program,
        Err(error) => {
            eprintln!("media-browser: finding this program to run it as a child: {error}");
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

/// The loop under [`supervise`]: wait for the compositor, start the child,
/// and start it again after each `NO_WINDOW` exit that a new compositor
/// caused. The steps are parameters so a test runs the loop over real
/// processes and no socket.
pub fn respawn(
    mut start: impl FnMut() -> std::io::Result<Child>,
    mut compositor_answers: impl FnMut() -> bool,
    mut compositor: impl FnMut() -> Option<Identity>,
) -> i32 {
    loop {
        if !compositor_answers() {
            eprintln!(
                "media-browser: the compositor did not answer within {}s; exiting {NO_WINDOW} so the kubelet restarts this container",
                COMPOSITOR_RETURN_LIMIT.as_secs()
            );
            return NO_WINDOW;
        }
        let gave_window = compositor();
        let mut child = match start() {
            Ok(child) => child,
            Err(error) => {
                eprintln!("media-browser: starting the client: {error}");
                return 1;
            }
        };
        CHILD.store(child.id() as i32, Ordering::SeqCst);
        let status = child.wait();
        CHILD.store(0, Ordering::SeqCst);
        let code = match status {
            Ok(status) => exit_code(status),
            Err(error) => {
                eprintln!("media-browser: waiting for the client: {error}");
                return 1;
            }
        };
        if code != NO_WINDOW || STOPPING.load(Ordering::SeqCst) {
            return code;
        }
        if gave_window.is_none() || compositor() == gave_window {
            eprintln!(
                "media-browser: the client got no window from a compositor that still runs; exiting {NO_WINDOW} so the kubelet restarts this container"
            );
            return NO_WINDOW;
        }
        eprintln!(
            "media-browser: the client lost its window, and it starts again when the compositor answers"
        );
    }
}

/// The status a shell reports: the child's exit code, or 128 and the signal
/// that ended it.
fn exit_code(status: ExitStatus) -> i32 {
    match (status.code(), status.signal()) {
        (Some(code), _) => code,
        (None, Some(signal)) => 128 + signal,
        (None, None) => 1,
    }
}

/// The compositor's socket, as a Wayland client finds it: `WAYLAND_DISPLAY`
/// when it is a path, or the name inside `XDG_RUNTIME_DIR`.
fn compositor_socket() -> Option<PathBuf> {
    let display = PathBuf::from(std::env::var_os("WAYLAND_DISPLAY")?);
    if display.is_absolute() {
        return Some(display);
    }
    Some(PathBuf::from(std::env::var_os("XDG_RUNTIME_DIR")?).join(display))
}

/// One compositor: the device, the inode, and the change time of the socket it
/// bound. The kernel can give a new socket the inode of the one it replaced, so
/// the change time is what tells them apart.
pub type Identity = (u64, u64, i64, i64);

/// The compositor that answers on the socket now, or nothing when none
/// answers or the container names no socket.
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

/// Waits until the compositor accepts a connection, up to the limit. A
/// container with no socket to name has nothing to look at, so the child
/// starts and its own connect decides.
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

/// The running child's pid, and whether the kubelet asked the container to
/// stop. The signal handler reads both, so they are atomics.
static CHILD: AtomicI32 = AtomicI32::new(0);
static STOPPING: AtomicBool = AtomicBool::new(false);

/// Passes the kubelet's SIGTERM, and a SIGINT, to the child. The first
/// process of a container is pid 1 in the container's own process namespace,
/// and the kernel delivers no signal to pid 1 that it does not handle, so
/// without the handler the stop would wait out the grace period.
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

    // A child that exits with the code it is given.
    fn exits(code: i32) -> std::io::Result<Child> {
        Command::new("sh")
            .args(["-c", &format!("exit {code}")])
            .spawn()
    }

    // The compositors the loop reads, one per read, so a test states each
    // compositor restart.
    fn compositors(identities: Vec<Option<Identity>>) -> impl FnMut() -> Option<Identity> {
        let mut identities = identities.into_iter();
        move || identities.next().flatten()
    }

    #[test]
    fn a_child_that_a_new_compositor_took_the_window_from_starts_again() {
        let mut codes = vec![NO_WINDOW, NO_WINDOW, 0].into_iter();
        let mut starts = 0;

        // Each child's compositor is replaced while it runs.
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
    fn a_child_with_no_window_from_a_compositor_that_still_runs_ends_the_container() {
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
    fn a_container_that_names_no_socket_hands_a_lost_window_to_the_kubelet() {
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
    fn a_compositor_that_does_not_come_back_hands_the_wait_to_the_kubelet() {
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
