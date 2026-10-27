//! The Wayland half of the arranger: the binding of `liken_arrangement_v1` on
//! the display's own connection, and the thread that reads its events.
//!
//! The compositor tells clients apart by their connection, and it grants a
//! claim's arranger to a client of that claim. A second connection would be
//! a second client, so the display binds the protocol on the connection that
//! winit opened for its window. winit reads that connection's default queue
//! on its own thread, so the binding takes a queue of its own and a thread
//! that blocks on it. The arranger's requests go out from the frame loop,
//! because a proxy sends from any thread.
//!
//! winit owns the connection, so the binding stops its thread before winit
//! disconnects: `Link::close` destroys the arranger and waits for a round
//! trip on the binding's queue, and the thread ends on that answer. If the
//! process ends without a close, the compositor sees the client go and
//! returns every surface of the claim to the default.

use std::fmt;
use std::sync::{Arc, Mutex, mpsc};
use std::time::Duration;

use iced::window::raw_window_handle::RawDisplayHandle;
use tokio::sync::mpsc::UnboundedSender;
use wayland_backend::client::Backend;
use wayland_client::protocol::{wl_callback, wl_registry};
use wayland_client::{Connection, Dispatch, EventQueue, QueueHandle, WEnum};

use super::protocol::liken_arranger_manager_v1::LikenArrangerManagerV1;
use super::protocol::liken_arranger_v1::{self, LikenArrangerV1};
use super::{Commit, Curve, Event, Fit, Refusal};

/// The global the compositor module offers.
const MANAGER: &str = "liken_arranger_manager_v1";

/// How long a close waits for the compositor to answer its round trip. The
/// answer takes one trip over a local socket, so this bounds a compositor
/// that stopped reading.
const CLOSE_WAIT: Duration = Duration::from_secs(1);

/// The binding of one connection, which the frame loop holds to send
/// arrangements and to close.
#[derive(Clone)]
pub struct Link {
    inner: Arc<Inner>,
}

impl fmt::Debug for Link {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("Link")
    }
}

struct Inner {
    connection: Connection,
    queue: QueueHandle<State>,
    shared: Mutex<Shared>,
    /// Answers once when the thread ends. The first close takes it.
    ended: Mutex<Option<mpsc::Receiver<()>>>,
}

/// What the thread and the frame loop share: the proxies once bound, and
/// whether a close has begun, so the thread binds nothing after one.
#[derive(Default)]
struct Shared {
    bound: Option<Bound>,
    closing: bool,
}

struct Bound {
    manager: LikenArrangerManagerV1,
    arranger: LikenArrangerV1,
}

/// Bind the protocol on winit's connection. It runs on winit's thread,
/// inside `iced::window::run`, so it only wraps the connection and starts
/// the thread, and the thread makes every blocking round trip.
pub fn attach(
    window: &dyn iced::window::Window,
    events: UnboundedSender<Event>,
) -> Result<Link, String> {
    let handle = window
        .display_handle()
        .map_err(|error| format!("the window gives no display handle: {error}"))?;
    let RawDisplayHandle::Wayland(handle) = handle.as_raw() else {
        return Err(format!(
            "the window is not on Wayland: {:?}",
            handle.as_raw()
        ));
    };
    // SAFETY: winit owns the wl_display and keeps it connected while the
    // event loop runs. The display closes the link before it asks the
    // toolkit to exit, and the thread ends before the close returns, so
    // nothing reads the connection after winit disconnects it.
    let backend = unsafe { Backend::from_foreign_display(handle.display.as_ptr().cast()) };
    Link::start(Connection::from_backend(backend), events)
        .map_err(|error| format!("the arranger's thread did not start: {error}"))
}

impl Link {
    /// Start the thread that binds the arranger on `connection` and sends
    /// every event it reads to `events`.
    pub fn start(connection: Connection, events: UnboundedSender<Event>) -> std::io::Result<Self> {
        let queue = connection.new_event_queue::<State>();
        let (done, ended) = mpsc::channel();
        let inner = Arc::new(Inner {
            connection,
            queue: queue.handle(),
            shared: Mutex::new(Shared::default()),
            ended: Mutex::new(Some(ended)),
        });
        let thread = Arc::clone(&inner);
        std::thread::Builder::new()
            .name("media-display-arranger".to_string())
            .spawn(move || {
                let mut state = State {
                    events,
                    manager: None,
                    stopped: false,
                };
                serve(&thread, queue, &mut state);
                let _ = done.send(());
            })?;
        Ok(Self { inner })
    }

    /// Send one arrangement. It fails before the thread has bound the
    /// arranger, and when the connection does not take the requests.
    pub fn commit(&self, commit: &Commit) -> Result<(), String> {
        {
            let shared = self
                .inner
                .shared
                .lock()
                .expect("no thread panics with the link");
            let bound = shared.bound.as_ref().ok_or("the arranger is not bound")?;
            for entry in &commit.arrangement.entries {
                bound.arranger.entry(
                    entry.app_id.to_string(),
                    entry.x,
                    entry.y,
                    entry.width,
                    entry.height,
                    entry.opacity,
                    fit(entry.fit),
                    entry.delay_ms,
                );
            }
            bound.arranger.commit(
                commit.serial,
                commit.arrangement.name.to_string(),
                commit.arrangement.duration_ms,
                curve(commit.arrangement.curve),
            );
        }
        self.inner
            .connection
            .flush()
            .map_err(|error| error.to_string())
    }

    /// Destroy the arranger and the manager, and stop the thread. It answers
    /// whether the thread ended within `CLOSE_WAIT`. A second close only
    /// answers true.
    pub fn close(&self) -> bool {
        let bound = {
            let mut shared = self
                .inner
                .shared
                .lock()
                .expect("no thread panics with the link");
            shared.closing = true;
            shared.bound.take()
        };
        if let Some(bound) = bound {
            bound.arranger.destroy();
            bound.manager.destroy();
        }
        let Some(ended) = self
            .inner
            .ended
            .lock()
            .expect("no thread panics with the link")
            .take()
        else {
            return true;
        };
        // The compositor answers requests in order, so the answer to this
        // sync arrives after it took the destroys, and the thread ends on it.
        self.inner
            .connection
            .display()
            .sync(&self.inner.queue, Stop);
        let _ = self.inner.connection.flush();
        ended.recv_timeout(CLOSE_WAIT).is_ok()
    }
}

/// The thread: list the globals, bind the arranger if the compositor offers
/// one, and read events until a close's round trip or a failed connection.
fn serve(inner: &Inner, mut queue: EventQueue<State>, state: &mut State) {
    let handle = queue.handle();
    let registry = inner.connection.display().get_registry(&handle, ());
    if let Err(error) = queue.roundtrip(state) {
        let _ = state.events.send(Event::Lost(error.to_string()));
        return;
    }
    let Some(name) = state.manager else {
        let _ = state.events.send(Event::Absent);
        return;
    };
    {
        let mut shared = inner.shared.lock().expect("no thread panics with the link");
        if shared.closing {
            return;
        }
        let manager: LikenArrangerManagerV1 = registry.bind(name, 1, &handle, ());
        let arranger = manager.get_arranger(&handle, ());
        shared.bound = Some(Bound { manager, arranger });
    }
    if let Err(error) = inner.connection.flush() {
        let _ = state.events.send(Event::Lost(error.to_string()));
        return;
    }
    while !state.stopped {
        if let Err(error) = queue.blocking_dispatch(state) {
            let _ = state.events.send(Event::Lost(error.to_string()));
            return;
        }
    }
}

/// What the thread's queue dispatches into.
struct State {
    events: UnboundedSender<Event>,
    /// The registry name of the manager global, once the compositor lists it.
    manager: Option<u32>,
    stopped: bool,
}

/// The user data of the close's round trip.
struct Stop;

impl Dispatch<wl_registry::WlRegistry, ()> for State {
    fn event(
        state: &mut Self,
        _: &wl_registry::WlRegistry,
        event: wl_registry::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        if let wl_registry::Event::Global {
            name, interface, ..
        } = event
            && interface == MANAGER
        {
            state.manager = Some(name);
        }
    }
}

impl Dispatch<wl_callback::WlCallback, Stop> for State {
    fn event(
        state: &mut Self,
        _: &wl_callback::WlCallback,
        _: wl_callback::Event,
        _: &Stop,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        state.stopped = true;
    }
}

impl Dispatch<LikenArrangerManagerV1, ()> for State {
    fn event(
        _: &mut Self,
        _: &LikenArrangerManagerV1,
        _: <LikenArrangerManagerV1 as wayland_client::Proxy>::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
    }
}

impl Dispatch<LikenArrangerV1, ()> for State {
    fn event(
        state: &mut Self,
        _: &LikenArrangerV1,
        event: liken_arranger_v1::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let _ = state.events.send(translate(event));
    }
}

/// One event of the arranger in the display's own terms.
fn translate(event: liken_arranger_v1::Event) -> Event {
    use liken_arranger_v1::Event as Wire;
    match event {
        Wire::Granted => Event::Granted,
        Wire::Refused { reason } => Event::Refused(match reason {
            WEnum::Value(reason) => Refusal::from_wire(reason as u32),
            WEnum::Unknown(reason) => Refusal::Other(reason),
        }),
        Wire::Region { width, height } => Event::Region { width, height },
        Wire::Surfaces { app_id, count } => Event::Surfaces { app_id, count },
        Wire::Landed { serial } => Event::Landed(serial),
    }
}

fn fit(fit: Fit) -> liken_arranger_v1::Fit {
    match fit {
        Fit::Scale => liken_arranger_v1::Fit::Scale,
    }
}

fn curve(curve: Curve) -> liken_arranger_v1::Curve {
    match curve {
        Curve::Smoothstep => liken_arranger_v1::Curve::Smoothstep,
        Curve::EaseIn => liken_arranger_v1::Curve::EaseIn,
    }
}

#[cfg(test)]
mod tests {
    use std::os::unix::net::UnixStream;
    use std::time::Duration;

    use tokio::sync::mpsc::{UnboundedReceiver, unbounded_channel};

    use super::*;
    use crate::arranger::fakecompositor::{self, Log, Offer};
    use crate::arranger::{Arranger, VIDEO};

    /// A bound on every wait, so a binding that never answers fails the
    /// test in seconds.
    const WAIT: Duration = Duration::from_secs(5);

    /// A link on one end of a socket pair, with the fake compositor on the
    /// other.
    fn linked(offer: Offer) -> (Link, UnboundedReceiver<Event>, Log) {
        let (server, client) = UnixStream::pair().expect("a socket pair");
        let log = fakecompositor::serve(server, offer);
        let connection = Connection::from_socket(client).expect("a connection on the socket");
        let (sender, events) = unbounded_channel();
        let link = Link::start(connection, sender).expect("the thread starts");
        (link, events, log)
    }

    async fn next(events: &mut UnboundedReceiver<Event>) -> Event {
        tokio::time::timeout(WAIT, events.recv())
            .await
            .expect("the compositor answered in time")
            .expect("the link is open")
    }

    async fn first(events: &mut UnboundedReceiver<Event>, count: usize) -> Vec<Event> {
        let mut read = Vec::new();
        for _ in 0..count {
            read.push(next(events).await);
        }
        read
    }

    fn requests(log: &Log) -> Vec<String> {
        log.lock().expect("the log").clone()
    }

    #[tokio::test]
    async fn a_grant_reports_the_region_and_the_film() {
        let (link, mut events, _) = linked(Offer::Grant(1920, 1080));
        assert_eq!(
            first(&mut events, 3).await,
            [
                Event::Granted,
                Event::Region {
                    width: 1920,
                    height: 1080
                },
                Event::Surfaces {
                    app_id: VIDEO.to_string(),
                    count: 1
                },
            ]
        );
        assert!(link.close());
    }

    #[tokio::test]
    async fn a_refusal_reports_its_reason() {
        let (link, mut events, _) = linked(Offer::Refuse(1));
        assert_eq!(next(&mut events).await, Event::Refused(Refusal::Busy));
        assert!(link.close());
    }

    #[tokio::test]
    async fn a_compositor_with_no_arranger_reports_it_absent() {
        let (link, mut events, _) = linked(Offer::Nothing);
        assert_eq!(next(&mut events).await, Event::Absent);
        assert!(link.close());
    }

    #[tokio::test]
    async fn a_compositor_that_goes_away_reports_the_link_lost() {
        let (link, mut events, _) = linked(Offer::GrantThenClose);
        assert_eq!(next(&mut events).await, Event::Granted);
        assert!(matches!(next(&mut events).await, Event::Lost(_)));
        assert!(link.close());
    }

    /// The step back reaches the compositor as two entries and a commit,
    /// and the compositor's landing comes back with the commit's serial.
    #[tokio::test]
    async fn a_commit_sends_its_entries_and_lands() {
        let (link, mut events, log) = linked(Offer::Grant(1920, 1080));
        let mut arranger = Arranger::default();
        for event in first(&mut events, 3).await {
            arranger.on_event(event);
        }
        let commit = arranger
            .step_back("the card rose")
            .commit
            .expect("a granted arranger steps back");
        link.commit(&commit)
            .expect("the connection takes the commit");

        assert_eq!(next(&mut events).await, Event::Landed(commit.serial));
        assert_eq!(
            requests(&log),
            [
                "entry liken-media-video 96,270 960x540 opacity 1 fit 0 delay 0",
                "entry liken-media-overlay 0,0 1920x1080 opacity 1 fit 0 delay 0",
                "commit 1 stepped-back 900 ms curve 0",
            ]
        );
        assert!(link.close());
    }

    /// The hand-off reaches the compositor as the film at opacity 0 on the
    /// ease-in curve, which the protocol numbers 2.
    #[tokio::test]
    async fn a_hand_off_sends_the_film_at_opacity_zero_on_ease_in() {
        let (link, mut events, log) = linked(Offer::Grant(1920, 1080));
        first(&mut events, 3).await;
        let commit = Commit {
            serial: 2,
            arrangement: crate::arranger::handing_off(1920, 1080),
        };
        link.commit(&commit)
            .expect("the connection takes the commit");

        assert_eq!(next(&mut events).await, Event::Landed(2));
        assert_eq!(
            requests(&log),
            [
                "entry liken-media-video 96,270 960x540 opacity 0 fit 0 delay 0",
                "entry liken-media-overlay 0,0 1920x1080 opacity 1 fit 0 delay 0",
                "commit 2 handing-off 300 ms curve 2",
            ]
        );
        assert!(link.close());
    }

    /// The close destroys the arranger before the manager, and the thread
    /// ends before the close returns, so winit can disconnect after it.
    #[tokio::test]
    async fn a_close_destroys_the_arranger_and_ends_the_thread() {
        let (link, mut events, log) = linked(Offer::Grant(1920, 1080));
        first(&mut events, 3).await;
        assert!(link.close());
        assert_eq!(requests(&log), ["arranger destroy", "manager destroy"]);
        assert!(link.close(), "a second close answers at once");
        assert!(link.commit(&commit()).is_err());
    }

    fn commit() -> Commit {
        Commit {
            serial: 1,
            arrangement: crate::arranger::restored(),
        }
    }
}
