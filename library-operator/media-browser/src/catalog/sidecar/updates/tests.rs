use std::io::{ErrorKind, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, mpsc};
use std::thread;
use std::time::{Duration, Instant};

use tempfile::TempDir;

use crate::catalog::Source;
use crate::catalog::sidecar::SidecarSource;

// One event as the pinned Corrosion agent sent it, captured from
// `/v1/updates/movies` on the local harness.
const EVENT: &str = r#"{"notify":["update",["default/scratch-drill","movie:tmdb:603"]]}"#;

const DEADLINE: Duration = Duration::from_secs(10);

fn within(deadline: Duration, mut check: impl FnMut() -> bool) -> bool {
    let end = Instant::now() + deadline;
    while Instant::now() < end {
        if check() {
            return true;
        }
        thread::sleep(Duration::from_millis(20));
    }
    false
}

// The fake agent answers just enough HTTP to stream the captured event
// format, so these tests prove the client against the wire shape and
// not against a mock of the client's own parser.
struct FakeAgent {
    port: u16,
    requests: Arc<Mutex<Vec<String>>>,
    movies: Arc<Mutex<Vec<TcpStream>>>,
    stop: Arc<AtomicBool>,
}

impl FakeAgent {
    // drop_movies is how many movies subscriptions to cut right after
    // their headers, so a test can force a reconnect.
    fn start(drop_movies: usize) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        listener.set_nonblocking(true).unwrap();
        let requests = Arc::new(Mutex::new(Vec::new()));
        let movies = Arc::new(Mutex::new(Vec::new()));
        let stop = Arc::new(AtomicBool::new(false));
        let agent = Self {
            port,
            requests: requests.clone(),
            movies: movies.clone(),
            stop: stop.clone(),
        };
        thread::spawn(move || serve(listener, requests, movies, stop, drop_movies));
        agent
    }

    fn base(&self) -> String {
        format!("http://127.0.0.1:{}", self.port)
    }

    fn requests(&self) -> Vec<String> {
        self.requests.lock().unwrap().clone()
    }

    fn movies_streams(&self) -> usize {
        self.movies.lock().unwrap().len()
    }

    fn send_event(&self) {
        let payload = format!("{EVENT}\n");
        let chunk = format!("{:x}\r\n{payload}\r\n", payload.len());
        for stream in self.movies.lock().unwrap().iter_mut() {
            let _ = stream.write_all(chunk.as_bytes());
            let _ = stream.flush();
        }
    }
}

impl Drop for FakeAgent {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
    }
}

fn serve(
    listener: TcpListener,
    requests: Arc<Mutex<Vec<String>>>,
    movies: Arc<Mutex<Vec<TcpStream>>>,
    stop: Arc<AtomicBool>,
    drop_movies: usize,
) {
    let end = Instant::now() + Duration::from_secs(30);
    let mut held = Vec::new();
    let mut dropped = 0;
    while !stop.load(Ordering::Acquire) && Instant::now() < end {
        match listener.accept() {
            Ok((mut stream, _)) => {
                stream
                    .set_read_timeout(Some(Duration::from_secs(1)))
                    .unwrap();
                let Some(request) = request_line(&mut stream) else {
                    continue;
                };
                requests.lock().unwrap().push(request.clone());
                let head = "HTTP/1.1 200 OK\r\n\
                            corro-query-id: 783bba7d-3bfe-4806-b6a6-c60c0e5c7c64\r\n\
                            transfer-encoding: chunked\r\n\r\n";
                let _ = stream.write_all(head.as_bytes());
                let _ = stream.flush();
                if request.contains("/v1/updates/movies") {
                    if dropped < drop_movies {
                        dropped += 1;
                        let _ = stream.shutdown(Shutdown::Both);
                        continue;
                    }
                    movies.lock().unwrap().push(stream);
                } else {
                    held.push(stream);
                }
            }
            Err(error) if error.kind() == ErrorKind::WouldBlock => {
                thread::sleep(Duration::from_millis(10));
            }
            Err(_) => break,
        }
    }
}

fn request_line(stream: &mut TcpStream) -> Option<String> {
    let mut head = Vec::new();
    let mut buffer = [0u8; 512];
    while !head.windows(4).any(|window| window == b"\r\n\r\n") {
        match stream.read(&mut buffer) {
            Ok(0) | Err(_) => return None,
            Ok(count) => head.extend_from_slice(&buffer[..count]),
        }
        if head.len() > 8192 {
            return None;
        }
    }
    let text = String::from_utf8_lossy(&head);
    Some(text.lines().next().unwrap_or_default().to_string())
}

fn source_against(agent: &FakeAgent, dir: &TempDir) -> SidecarSource {
    SidecarSource::new(dir.path().join("catalog.db"), &agent.base())
}

#[test]
fn the_source_posts_one_subscription_per_item_table() {
    let agent = FakeAgent::start(0);
    let dir = TempDir::new().unwrap();
    let _source = source_against(&agent, &dir);

    let subscribed = within(DEADLINE, || {
        let requests = agent.requests();
        ["movies", "series", "episodes"].iter().all(|table| {
            requests
                .iter()
                .any(|line| line.starts_with(&format!("POST /v1/updates/{table} ")))
        })
    });
    assert!(subscribed, "requests seen: {:?}", agent.requests());
}

#[test]
fn an_event_marks_changed_and_fires_the_waker() {
    let agent = FakeAgent::start(0);
    let dir = TempDir::new().unwrap();
    let mut source = source_against(&agent, &dir);

    let (sender, receiver) = mpsc::channel();
    source.wake_by(Arc::new(move || {
        let _ = sender.send(());
    }));
    assert!(!source.changed());

    assert!(within(DEADLINE, || agent.movies_streams() >= 1));
    agent.send_event();

    receiver.recv_timeout(DEADLINE).unwrap();
    assert!(source.changed());
    assert!(!source.changed());
}

#[test]
fn a_dropped_stream_reconnects_and_marks_everything_changed() {
    let agent = FakeAgent::start(1);
    let dir = TempDir::new().unwrap();
    let mut source = source_against(&agent, &dir);

    let reconnected = within(DEADLINE, || {
        agent
            .requests()
            .iter()
            .filter(|line| line.contains("/v1/updates/movies"))
            .count()
            >= 2
    });
    assert!(reconnected, "requests seen: {:?}", agent.requests());
    assert!(within(DEADLINE, || source.changed()));
}
