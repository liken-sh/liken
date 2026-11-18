//! The MQTT connection of the crate. The reader thread owns the connection,
//! subscribes at the start of each session, and passes each message to the
//! [`Screen`] rules. The clock thread fires the rules' timers: the fade
//! timer that puts the screen to sleep and the off timer that requests the
//! panel off. Both threads publish what the rules produce on the same
//! connection.
//!
//! A client keeps a [`Reader`], calls [`Bus::drain`] each time its loop
//! wakes, and draws the [`Moment`]s it returns. The client opens no
//! connection and does not need this crate's topic names.

use std::sync::mpsc;
use std::sync::{Arc, Mutex, Weak};
use std::time::{Duration, Instant};

// `rumqttc`'s `Client` is renamed so that it is not mistaken for the
// client program that uses this crate.
use rumqttc::{
    Client as Broker, ConnectionError, Event, MqttOptions, Packet, QoS, SubscribeFilter,
};

use crate::screen::{Effect, Moment, Publish, Screen};
use crate::wiring::Wiring;

mod alarm;

use alarm::Alarm;

const DEFAULT_PORT: u16 = 1883;

/// The same keepalive as `busKeepalive` in `media-operator/bus.go`.
const KEEPALIVE: Duration = Duration::from_secs(30);

/// The largest packet this client sends or accepts. rumqttc limits both
/// directions to 10 KB by default, and a play request for a whole season of
/// episodes, with the items that follow it, is larger than that. The broker
/// has its own limit. `media-operator/deploy/bus.yaml` sets no
/// `max_packet_size`, so Mosquitto uses its default, which the
/// `mosquitto.conf` manual gives as larger than this value.
const MAX_PACKET_SIZE: usize = 256 * 1024;

/// After a failed session, the reader thread waits before it reconnects, so
/// a broker that is down is not redialed in a tight loop. The wait starts at
/// `RECONNECT_MIN`, doubles on each failure up to `RECONNECT_MAX`, and
/// resets when a session connects. `media-operator/bus.go` uses the same
/// limits.
const RECONNECT_MIN: Duration = Duration::from_secs(1);
const RECONNECT_MAX: Duration = Duration::from_secs(30);

/// The capacity of `rumqttc`'s request queue of subscribes and publishes.
/// A publish that finds the queue full is logged and dropped. The channel
/// of moments to the client is unbounded, so no moment is dropped.
const QUEUE_DEPTH: usize = 64;

/// A function that wakes the client's event loop. It can be called from any
/// thread.
pub type Waker = Arc<dyn Fn() + Send + Sync>;

/// What a client needs from the bus. A client's tests can supply their own
/// implementation, which feeds in real moments and records the requests
/// without a socket.
pub trait Bus: std::fmt::Debug {
    /// Returns the moments that arrived since the last call. It never
    /// blocks.
    fn drain(&self) -> Vec<Moment>;

    /// Puts the screen to sleep, for a client that handles key presses
    /// itself. The idle screen calls it on a back press. A client with
    /// nested screens calls it only on a back press at its top screen,
    /// because only the client can tell whether back has a screen to return
    /// to.
    fn sleep(&self);

    /// Publishes one payload on a topic that the client owns, over this
    /// crate's connection, so a client that sends its own requests, such as
    /// the library browser requesting a `Play`, opens no second connection
    /// with a second client identifier. The [`Screen`] rules ignore the
    /// topic. Messages on it reach the client only when the client passed
    /// the topic to [`Reader::open`].
    fn publish(&self, topic: &str, payload: Vec<u8>, retained: bool);

    /// Registers a function that the threads call after they send moments,
    /// so the client draws a key press on its next frame. Without a waker, a
    /// moment waits in the channel until the client's loop wakes on its own.
    fn wake_on_delivery(&self, wake: Waker);
}

/// The client registers its waker after the threads start, so the threads
/// read it from here each time they send moments.
type SharedWaker = Arc<Mutex<Option<Waker>>>;

/// The client's handle to the bus connection. Dropping it ends both
/// threads.
pub struct Reader {
    moments: mpsc::Receiver<Moment>,
    /// The only strong reference to the rules. The threads hold `Weak` ones
    /// and end when they can no longer upgrade them.
    screen: Arc<Mutex<Screen>>,
    threads: Threads,
}

impl std::fmt::Debug for Reader {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Reader").finish_non_exhaustive()
    }
}

impl Reader {
    /// Connects to the broker in `wiring` and subscribes. It returns `None`
    /// when `wiring` has no broker address, when there is no topic to
    /// subscribe to, or when a thread fails to start. A client with no
    /// reader runs on a workstation with only its built-in content.
    ///
    /// Build `client_id` with [`client_id`], because a broker closes the
    /// older connection when two connections use one identifier.
    ///
    /// The reader subscribes to `client_topics` in each session, beside the
    /// screen's own topics, and each message on one arrives as a
    /// [`Moment::Message`]. A client that passes one of its own topics gets a
    /// reader even when `wiring` has no topics.
    pub fn open(wiring: &Wiring, client_id: &str, client_topics: &[String]) -> Option<Self> {
        let screen = Screen::new(wiring).reading(client_topics);
        let (host, port) = broker(&wiring.bus_address)?;
        if screen.filters().is_empty() {
            return None;
        }

        let mut options = MqttOptions::new(client_id, host, port);
        options.set_keep_alive(KEEPALIVE);
        options.set_max_packet_size(MAX_PACKET_SIZE, MAX_PACKET_SIZE);
        let (client, connection) = Broker::new(options, QUEUE_DEPTH);
        // `media_bus_connected` reads 0 until the first session connects. The
        // metric is absent when `open` returned `None` before this line:
        // the broker address is unset or does not parse, or there is no
        // topic to subscribe to.
        crate::metrics::bus_connected(false);

        let (sender, moments) = mpsc::channel();
        let screen = Arc::new(Mutex::new(screen));
        let threads = Threads {
            screen: Arc::downgrade(&screen),
            client,
            sender,
            waker: Arc::default(),
            alarm: Arc::default(),
        };

        // The timers fire on their own thread because the reader thread
        // blocks in the socket read. Interrupting that read to fire a timer
        // could lose a message that was partly read.
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
    fn drain(&self) -> Vec<Moment> {
        self.moments.try_iter().collect()
    }

    /// The resulting [`Moment::Sleep`] comes back through [`Bus::drain`] like
    /// every other moment, so the client handles one stream.
    fn sleep(&self) {
        let effects = run_rule(&self.screen, &self.threads.alarm, |screen| {
            screen.sleep(Instant::now())
        });
        perform(&self.threads, effects);
    }

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

    fn wake_on_delivery(&self, wake: Waker) {
        *self
            .threads
            .waker
            .lock()
            .expect("the waker lock is poisoned") = Some(wake);
    }
}

/// The reader thread ends at its next event, when it finds the rules gone.
/// The clock thread can be waiting with no timeout, so the drop closes the
/// alarm to wake it.
impl Drop for Reader {
    fn drop(&mut self) {
        self.threads.alarm.close();
    }
}

/// The state that the reader thread and the clock thread share.
#[derive(Clone)]
struct Threads {
    screen: Weak<Mutex<Screen>>,
    client: Broker,
    sender: mpsc::Sender<Moment>,
    waker: SharedWaker,
    alarm: Arc<Alarm>,
}

impl Threads {
    fn screen(&self) -> Option<Arc<Mutex<Screen>>> {
        self.screen.upgrade()
    }
}

/// Starts a named thread. A failure is logged, because the client then
/// gets no reader and runs without the bus until the pod restarts.
fn spawn(name: &str, body: impl FnOnce() + Send + 'static) -> Option<()> {
    std::thread::Builder::new()
        .name(name.into())
        .spawn(body)
        .inspect_err(|error| eprintln!("media-screen: starting the {name} thread: {error}"))
        .ok()
        .map(|_| ())
}

/// The reader thread's loop. It subscribes on each CONNACK, because the
/// broker discards the subscriptions when a session ends. Each message goes
/// through the rules, and only the moments they produce reach the client.
fn read(threads: &Threads, events: impl Iterator<Item = Result<Event, ConnectionError>>) {
    let mut backoff = RECONNECT_MIN;
    for event in events {
        let Some(screen) = threads.screen() else {
            return;
        };
        let effects = match event {
            Ok(Event::Incoming(Packet::ConnAck(_))) => {
                crate::metrics::bus_connected(true);
                backoff = RECONNECT_MIN;
                run_rule(&screen, &threads.alarm, |screen| {
                    let effects = screen.connected();
                    // QoS 0, as `media-operator/bus.go` uses. A message lost
                    // with a failed session is not sent again. That is
                    // acceptable, because the broker sends each retained
                    // message again on subscribe, and every publisher
                    // publishes its retained state again when it connects.
                    let filters = screen.filters().into_iter().map(|path| SubscribeFilter {
                        path,
                        qos: QoS::AtMostOnce,
                    });
                    // `try_subscribe_many` queues the request. A blocking send
                    // would wait for the connection to be read, and this
                    // thread is what reads it.
                    let _ = threads.client.try_subscribe_many(filters);
                    effects
                })
            }
            Ok(Event::Incoming(Packet::Publish(message))) => {
                run_rule(&screen, &threads.alarm, |screen| {
                    screen.deliver(
                        &message.topic,
                        &message.payload,
                        message.retain,
                        Instant::now(),
                    )
                })
            }
            Err(error) => {
                crate::metrics::bus_connected(false);
                // The iterator reconnects when the loop requests the next
                // event, so the thread waits out the backoff first.
                eprintln!("media-screen: bus connection: {error}");
                std::thread::sleep(backoff);
                backoff = next_backoff(backoff);
                Vec::new()
            }
            Ok(_) => Vec::new(),
        };
        if !perform(threads, effects) {
            // The client dropped its end of the channel, so nothing reads
            // what this thread produces.
            return;
        }
    }
}

/// The clock thread's loop. It sleeps until the next timer's deadline, or
/// with no timeout while no timer is armed, so an idle screen does not wake
/// it. A thread that arms a timer or moves one earlier signals the alarm, and
/// the clock reads the deadline again.
fn clock(threads: &Threads) {
    loop {
        let Some(screen) = threads.screen() else {
            return;
        };
        let signals = threads.alarm.signal_count();
        let deadline = screen
            .lock()
            .expect("the screen lock is poisoned")
            .next_deadline();
        // Drop the strong reference before waiting, or the rules stay alive
        // after the client drops its reader.
        drop(screen);
        if !threads.alarm.wait(signals, deadline) {
            return;
        }

        let Some(screen) = threads.screen() else {
            return;
        };
        let effects = run_rule(&screen, &threads.alarm, |screen| {
            screen.tick(Instant::now())
        });
        if !perform(threads, effects) {
            return;
        }
    }
}

fn next_backoff(backoff: Duration) -> Duration {
    (backoff * 2).min(RECONNECT_MAX)
}

/// Runs `rule` under the lock, and logs the lines it produced before the
/// lock is released, so the lines of both threads appear in the order the
/// rules ran.
///
/// When the next deadline moves earlier, `run_rule` signals the alarm so the
/// clock waits for the new deadline. A deadline that moves later needs no
/// signal, because the clock wakes at the earlier one, finds nothing due, and
/// waits again.
fn run_rule(
    screen: &Mutex<Screen>,
    alarm: &Alarm,
    rule: impl FnOnce(&mut Screen) -> Vec<Effect>,
) -> Vec<Effect> {
    let mut screen = screen.lock().expect("the screen lock is poisoned");
    let before = screen.next_deadline();
    let effects = rule(&mut screen);
    for line in screen.take_lines() {
        eprintln!("media-screen: {line}");
    }
    let earlier = screen
        .next_deadline()
        .is_some_and(|at| before.is_none_or(|then| at < then));
    drop(screen);
    if earlier {
        alarm.signal();
    }
    effects
}

/// Sends each moment to the client and queues each publish. Then, when any
/// moment was sent, it calls the client's waker once, so the client handles
/// all the moments of one rule on one wake. It returns false when the client
/// dropped its receiver, and the calling thread then ends.
fn perform(threads: &Threads, effects: Vec<Effect>) -> bool {
    let mut sent_moment = false;
    for effect in effects {
        match effect {
            Effect::Moment(moment) => {
                if threads.sender.send(moment).is_err() {
                    return false;
                }
                sent_moment = true;
            }
            Effect::Publish(publish) => send(&threads.client, publish),
        }
    }
    if sent_moment
        && let Some(wake) = threads
            .waker
            .lock()
            .expect("the waker lock is poisoned")
            .as_ref()
    {
        wake();
    }
    true
}

/// Queues one publish at QoS 0. The reader thread drives the connection and
/// writes it.
fn send(client: &Broker, publish: Publish) {
    if let Err(error) = client.try_publish(
        publish.topic,
        QoS::AtMostOnce,
        publish.retained,
        publish.payload,
    ) {
        eprintln!("media-screen: publishing to the bus: {error}");
    }
}

/// Builds the MQTT client identifier from `prefix`, the client's own name,
/// and `hostname`, which in a pod is the pod's name. So each replica of a
/// client connects under its own identifier.
pub fn client_id(prefix: &str, hostname: &str) -> String {
    match hostname.trim() {
        "" => prefix.to_string(),
        host => format!("{prefix}-{host}"),
    }
}

/// Reads the host name from `/etc/hostname`, or returns an empty string when
/// the file is missing. In a pod it is the pod's name.
pub fn hostname() -> String {
    std::fs::read_to_string("/etc/hostname").unwrap_or_default()
}

/// Splits a broker address into host and port. An address with no port gets
/// `DEFAULT_PORT`. An empty address, or one with an unparseable port,
/// returns `None`.
///
/// An IPv6 literal contains colons, so its port is read only after the
/// closing bracket (`[::1]:1884`). A literal without brackets is taken as
/// the whole host, with the default port.
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
