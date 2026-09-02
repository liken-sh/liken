//! The socket half of the crate: the thread that holds the connection, the
//! subscribe it sends on every session, the clock that runs the two windows,
//! and the publishes the rules ask for.
//!
//! The client sees none of this. It holds a [`Reader`], calls
//! [`Bus::drain`] on every wake of its loop, and draws what comes back.
//! Every publish this crate makes goes out on the connection these
//! threads already hold, so the client opens nothing and names no topic
//! of the crate's.

use std::sync::mpsc;
use std::sync::{Arc, Mutex, Weak};
use std::time::{Duration, Instant};

// `rumqttc`'s client type is imported under the broker's name, because this
// crate holds a `Screen` of its own and one file must not read as if it spoke
// about both.
use rumqttc::{
    Client as Broker, ConnectionError, Event, MqttOptions, Packet, QoS, SubscribeFilter,
};

use crate::screen::{Effect, Moment, Publish, Screen};
use crate::wiring::Wiring;

/// The port a broker answers on when the address names none.
const DEFAULT_PORT: u16 = 1883;

/// The keepalive this client asks for. It is the interval `media-operator`'s
/// own bus client asks for, so every client of one broker keeps the same
/// clock.
const KEEPALIVE: Duration = Duration::from_secs(30);

/// The wait after a failed session, so a broker that is down is no tight
/// reconnect loop.
const RECONNECT_WAIT: Duration = Duration::from_secs(1);

/// The longest the clock sleeps between two reads of the armed window. The
/// client asks for the shade on its own thread, and that request arms the off
/// window, so the clock reads the deadline again on this cadence rather than
/// sleeping out the window it started on. One wake a second is the rate the
/// idle screen already redraws its clock at.
const TICK: Duration = Duration::from_secs(1);

/// The capacity of `rumqttc`'s outbound request queue, which carries the
/// subscribes and every publish the rules ask for. The inbound path to the
/// client is an unbounded channel, and it drops nothing.
const QUEUE_DEPTH: usize = 64;

/// A handle that wakes the client's event loop from any thread.
pub type Waker = Arc<dyn Fn() + Send + Sync>;

/// What a client needs from the bus. It is a trait so a client's own tests
/// fold real moments and see a real request with no socket under them.
pub trait Bus: std::fmt::Debug {
    /// Every moment that arrived since the last call. The call never blocks.
    fn drain(&self) -> Vec<Moment>;

    /// Ask for the shade, from the client's own reading of a press. The stock
    /// idle client asks on back; a client with levels asks at its top level,
    /// because only the client knows whether back has anywhere to go.
    fn sleep(&self);

    /// Publish one payload on a topic of the client's own, on the
    /// connection this crate already holds. A client with a request of
    /// its own, such as the library layer's browser asking for a
    /// `Play`, must not open a second connection to the same broker
    /// under a second identifier. The crate does nothing with the
    /// topic: the rules in [`Screen`] neither read it nor subscribe to
    /// it.
    fn publish(&self, topic: &str, payload: Vec<u8>, retained: bool);

    /// Wake the loop on every delivery, so a press shows on the next frame
    /// rather than at the next scheduled second.
    fn wake_on_delivery(&self, wake: Waker);
}

/// The slot the threads read their waker from. The loop does not exist yet
/// when the reader connects, so the waker arrives after the threads start,
/// and each one reads the slot on every delivery.
type WakerSlot = Arc<Mutex<Option<Waker>>>;

/// The subscription, held by the client.
pub struct Reader {
    moments: mpsc::Receiver<Moment>,
    /// The rules. Both threads hold a weak reference to this one value, so
    /// dropping the reader ends both of them, and a client that opened a
    /// reader and let it go leaves no thread behind.
    screen: Arc<Mutex<Screen>>,
    threads: Threads,
}

impl std::fmt::Debug for Reader {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Reader").finish_non_exhaustive()
    }
}

impl Reader {
    /// Connect to the broker the wiring names and subscribe. The answer is
    /// `None` when the operator named no broker or no topics, which is how a
    /// client runs on a workstation with its seeds alone.
    ///
    /// `client_id` must name this client alone, because a broker closes the
    /// older connection when two arrive under one identifier.
    pub fn open(wiring: &Wiring, client_id: &str) -> Option<Self> {
        let screen = Screen::new(wiring);
        let (host, port) = broker(&wiring.bus_address)?;
        if screen.filters().is_empty() {
            return None;
        }

        let mut options = MqttOptions::new(client_id, host, port);
        options.set_keep_alive(KEEPALIVE);
        let (client, connection) = Broker::new(options, QUEUE_DEPTH);

        let (sender, moments) = mpsc::channel();
        let screen = Arc::new(Mutex::new(screen));
        let threads = Threads {
            screen: Arc::downgrade(&screen),
            client,
            sender,
            waker: Arc::default(),
        };

        // The connection thread never leaves its loop, so the two windows run
        // on a thread of their own. A deadline on the connection itself would
        // have to cancel a read of the socket to fire, and a cancelled read
        // can lose the press it was in the middle of.
        let reading = threads.clone();
        spawn("media-bus", move || {
            let mut connection = connection;
            read(&reading, connection.iter());
        })?;
        let ticking = threads.clone();
        spawn("media-clock", move || clock(&ticking))?;

        Some(Self {
            moments,
            screen,
            threads,
        })
    }
}

impl Bus for Reader {
    /// The call costs the decoding and nothing else, because the threads
    /// decoded every moment before the channel.
    fn drain(&self) -> Vec<Moment> {
        self.moments.try_iter().collect()
    }

    /// The shade comes back through [`Bus::drain`] the way every other moment
    /// does, so the client folds one stream.
    fn sleep(&self) {
        let effects = self
            .screen
            .lock()
            .expect("no thread panics with the lock")
            .sleep(Instant::now());
        perform(&self.threads, effects);
    }

    /// The publish is queued rather than sent, the way every publish the
    /// rules ask for is, because the reader thread is what drives the
    /// connection.
    fn publish(&self, topic: &str, payload: Vec<u8>, retained: bool) {
        send(
            &self.threads.client,
            Publish {
                topic: topic.to_string(),
                payload,
                retained,
            },
        );
    }

    /// Without a waker a moment waits in the channel for the next scheduled
    /// wake, and a press then shows up to a second late.
    fn wake_on_delivery(&self, wake: Waker) {
        *self
            .threads
            .waker
            .lock()
            .expect("no reader panics with the lock") = Some(wake);
    }
}

/// What each thread of this crate holds: the rules, the connection to publish
/// on, the channel to the client, and the slot its waker arrives in.
#[derive(Clone)]
struct Threads {
    screen: Weak<Mutex<Screen>>,
    client: Broker,
    sender: mpsc::Sender<Moment>,
    waker: WakerSlot,
}

impl Threads {
    /// The rules, while a client still holds them. A thread that reads
    /// nothing here ends, because the client its work is for is gone.
    fn screen(&self) -> Option<Arc<Mutex<Screen>>> {
        self.screen.upgrade()
    }
}

/// Start one of this crate's threads. A client that spawns none draws its
/// seeds and hears nothing for the life of the pod, so the line says why.
fn spawn(name: &str, body: impl FnOnce() + Send + 'static) -> Option<()> {
    std::thread::Builder::new()
        .name(name.into())
        .spawn(body)
        .inspect_err(|error| eprintln!("media-screen: {name}: {error}"))
        .ok()
        .map(|_| ())
}

/// The reader thread. It subscribes on every connection, because a broker
/// holds no subscription across a session, and it folds each message through
/// the rules before the channel, so the client's loop takes finished values.
fn read(threads: &Threads, events: impl Iterator<Item = Result<Event, ConnectionError>>) {
    for event in events {
        let Some(screen) = threads.screen() else {
            return;
        };
        let effects = match event {
            Ok(Event::Incoming(Packet::ConnAck(_))) => {
                let mut screen = screen.lock().expect("no thread panics with the lock");
                let effects = screen.connected();
                let filters = screen.filters().into_iter().map(|path| SubscribeFilter {
                    path,
                    qos: QoS::AtMostOnce,
                });
                // The subscribe is queued rather than sent, because this
                // thread is the one that drives the connection. A blocking
                // send would wait for a reader that is this loop.
                let _ = threads.client.try_subscribe_many(filters);
                effects
            }
            Ok(Event::Incoming(Packet::Publish(message))) => screen
                .lock()
                .expect("no thread panics with the lock")
                .deliver(
                    &message.topic,
                    &message.payload,
                    message.retain,
                    Instant::now(),
                ),
            Err(error) => {
                // The client reconnects on its own, so the line is the record
                // and not a request for anything.
                eprintln!("media-screen: bus: {error}");
                std::thread::sleep(RECONNECT_WAIT);
                Vec::new()
            }
            Ok(_) => Vec::new(),
        };
        if !perform(threads, effects) {
            // The client dropped its reader, so nothing reads what this
            // thread decodes.
            return;
        }
    }
}

/// The clock thread, which is the two windows. It sleeps to the armed
/// deadline and no longer than [`TICK`], so a deadline another thread armed
/// is read within the second.
fn clock(threads: &Threads) {
    loop {
        let Some(screen) = threads.screen() else {
            return;
        };
        let deadline = screen
            .lock()
            .expect("no thread panics with the lock")
            .next_deadline();
        // The rules are released before the sleep, so the reader thread and
        // the client both reach them while this one waits.
        drop(screen);
        std::thread::sleep(wait(deadline, Instant::now()));

        let Some(screen) = threads.screen() else {
            return;
        };
        let effects = screen
            .lock()
            .expect("no thread panics with the lock")
            .tick(Instant::now());
        if !perform(threads, effects) {
            return;
        }
    }
}

/// How long the clock sleeps: to the armed deadline, and never longer than
/// one tick, so a window another thread armed is read on the next pass.
fn wait(deadline: Option<Instant>, now: Instant) -> Duration {
    match deadline {
        Some(at) => at.saturating_duration_since(now).min(TICK),
        None => TICK,
    }
}

/// Perform one fold's effects: send each moment to the client, publish each
/// message on the connection, and wake the loop once if anything reached the
/// client. The answer is false only when the client dropped its receiver,
/// which ends the thread that called.
///
/// The wake follows the sends, so the loop reads a whole fold on one pass
/// rather than one moment per wake.
fn perform(threads: &Threads, effects: Vec<Effect>) -> bool {
    let mut drew = false;
    for effect in effects {
        match effect {
            Effect::Moment(moment) => {
                if threads.sender.send(moment).is_err() {
                    return false;
                }
                drew = true;
            }
            Effect::Publish(publish) => send(&threads.client, publish),
        }
    }
    if drew
        && let Some(wake) = threads
            .waker
            .lock()
            .expect("no client panics with the lock")
            .as_ref()
    {
        wake();
    }
    true
}

/// One publish, queued rather than sent, because the reader thread is what
/// drives the connection. It goes at QoS 0, and the rules say which messages
/// the broker retains.
fn send(client: &Broker, publish: Publish) {
    if let Err(error) = client.try_publish(
        publish.topic,
        QoS::AtMostOnce,
        publish.retained,
        publish.payload,
    ) {
        eprintln!("media-screen: bus: {error}");
    }
}

/// The identifier this client connects under. It must name this client alone,
/// because a broker closes the older connection when two arrive under one
/// identifier. `prefix` is the client's own name, so two different clients on
/// one machine do not collide either.
pub fn client_id(prefix: &str, hostname: &str) -> String {
    match hostname.trim() {
        "" => prefix.to_string(),
        host => format!("{prefix}-{host}"),
    }
}

/// The name this machine answers to. In a pod it is the pod's own name, which
/// is unique in the cluster.
pub fn hostname() -> String {
    std::fs::read_to_string("/etc/hostname").unwrap_or_default()
}

/// The broker's host and port. An address with no port answers on the MQTT
/// default. An empty address is no broker at all.
///
/// An IPv6 literal carries colons of its own, so the port follows the
/// brackets the URI form puts around the address, and a bare literal is the
/// host alone on the default port.
fn broker(address: &str) -> Option<(String, u16)> {
    let address = address.trim();
    if address.is_empty() {
        return None;
    }
    if let Some(rest) = address.strip_prefix('[') {
        let (host, after) = rest.split_once(']')?;
        return match after {
            "" => Some((host.to_string(), DEFAULT_PORT)),
            after => Some((host.to_string(), after.strip_prefix(':')?.parse().ok()?)),
        };
    }
    if address.matches(':').count() > 1 {
        return Some((address.to_string(), DEFAULT_PORT));
    }
    match address.rsplit_once(':') {
        Some((host, port)) => Some((host.to_string(), port.parse().ok()?)),
        None => Some((address.to_string(), DEFAULT_PORT)),
    }
}

#[cfg(test)]
mod tests;
