// The socket half of the bus: the thread that holds the connection, the
// subscribe it sends on every session, and the one request the browser
// publishes.

use std::sync::mpsc;
use std::time::Duration;

// Rumqttc's client type is imported under the broker's name, because
// this crate holds a Browser of its own and one file must not read as if it
// spoke about both.
use rumqttc::{
    Client as Broker, ConnectionError, Event, MqttOptions, Packet, QoS, SubscribeFilter,
};

use super::{Bus, Message, Session};
use crate::harness::Waker;

/// The port a broker answers on when the address names none.
const DEFAULT_PORT: u16 = 1883;

/// The keepalive this client asks for, the interval every other client
/// of one broker asks for.
const KEEPALIVE: Duration = Duration::from_secs(30);

/// The wait after a failed session, so a broker that is down is no
/// tight reconnect loop.
const RECONNECT_WAIT: Duration = Duration::from_secs(1);

/// The capacity of rumqttc's outbound request queue, which carries the
/// subscribes and the sleep request. The inbound path is unbounded and drops
/// nothing.
const QUEUE_DEPTH: usize = 64;

/// The one request a client at its top level makes; the command pod
/// reads it beside re-present and brings the shade down.
pub const SLEEP: &[u8] = br#"{"action":"sleep"}"#;

/// The slot the reader thread reads its waker from. The loop does not
/// exist yet when the reader connects.
type WakerSlot = std::sync::Arc<std::sync::Mutex<Option<Waker>>>;

/// The subscription, held by the browser. Dropping it closes the channel,
/// and the reader thread ends on its next delivery.
pub struct Reader {
    messages: mpsc::Receiver<Message>,
    waker: WakerSlot,
    /// The publishing half of the same connection the thread drives.
    client: Broker,
    commands: String,
}

impl std::fmt::Debug for Reader {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Reader").finish_non_exhaustive()
    }
}

impl Reader {
    /// Connect to the broker at `address` and subscribe. The answer is `None`
    /// when the operator named no broker or no topics, which is a run on a
    /// workstation with the keyboard alone.
    ///
    /// `client_id` must name this client alone, because a broker closes the
    /// older connection when two arrive under one identifier.
    pub fn open(address: &str, client_id: &str, commands: String, screen: String) -> Option<Self> {
        let (host, port) = broker(address)?;
        if commands.is_empty() || screen.is_empty() {
            return None;
        }
        let session = Session::new(commands.clone(), screen);

        let mut options = MqttOptions::new(client_id, host, port);
        options.set_keep_alive(KEEPALIVE);
        let (client, connection) = Broker::new(options, QUEUE_DEPTH);

        let (sender, messages) = mpsc::channel();
        let waker: WakerSlot = std::sync::Arc::default();
        let woken = std::sync::Arc::clone(&waker);
        let reading = client.clone();
        std::thread::Builder::new()
            .name("media-bus".into())
            .spawn(move || read(session, reading, connection, &sender, &woken))
            // A browser that spawns no reader takes the keyboard for the
            // life of the pod, so the log line says why.
            .inspect_err(|error| eprintln!("media-browser: bus: {error}"))
            .ok()?;

        Some(Self {
            messages,
            waker,
            client,
            commands,
        })
    }

    /// Publish one payload on the commands topic. The publish is queued rather
    /// than sent, because the reader thread is what drives the connection.
    pub fn publish(&self, payload: &'static [u8]) {
        if let Err(error) =
            self.client
                .try_publish(self.commands.as_str(), QoS::AtMostOnce, false, payload)
        {
            eprintln!("media-browser: bus: {error}");
        }
    }
}

impl Bus for Reader {
    fn drain(&self) -> Vec<Message> {
        self.messages.try_iter().collect()
    }

    fn request_sleep(&self) {
        self.publish(SLEEP);
    }

    fn wake_on_delivery(&self, wake: Waker) {
        *self.waker.lock().expect("no reader panics with the lock") = Some(wake);
    }
}

/// The reader thread. It subscribes on every connection, because a broker
/// holds no subscription across sessions, and it decodes each message before
/// the channel, so the frame loop takes finished values.
fn read(
    session: Session,
    client: Broker,
    mut connection: rumqttc::Connection,
    sender: &mpsc::Sender<Message>,
    waker: &WakerSlot,
) {
    for event in connection.iter() {
        match event {
            Ok(Event::Incoming(Packet::ConnAck(_))) => {
                let filters = session.filters().into_iter().map(|path| SubscribeFilter {
                    path,
                    qos: QoS::AtMostOnce,
                });
                // The subscribe is queued rather than sent, because
                // this thread is the one that drives the connection.
                let _ = client.try_subscribe_many(filters);
            }
            Ok(Event::Incoming(Packet::Publish(publish))) => {
                if !forward(&session, &publish.topic, &publish.payload, sender, waker) {
                    // The browser dropped its reader, so nothing reads what
                    // this thread decodes.
                    return;
                }
            }
            Err(error) => {
                report(&error);
                std::thread::sleep(RECONNECT_WAIT);
            }
            Ok(_) => {}
        }
    }
}

/// Decode one publish, hand it to the browser, and wake the loop. The
/// answer is false only when the browser dropped its receiver, which ends the
/// thread.
fn forward(
    session: &Session,
    topic: &str,
    payload: &[u8],
    sender: &mpsc::Sender<Message>,
    waker: &WakerSlot,
) -> bool {
    let Some(message) = session.deliver(topic, payload) else {
        return true;
    };
    if sender.send(message).is_err() {
        return false;
    }
    if let Some(wake) = waker
        .lock()
        .expect("no browser panics with the lock")
        .as_ref()
    {
        wake();
    }
    true
}

/// One line for a failed session. The client reconnects on its own, so
/// the line is the record and not a request for anything.
fn report(error: &ConnectionError) {
    eprintln!("media-browser: bus: {error}");
}

/// The identifier this client connects under. It must name this client
/// alone, because a broker closes the older connection when two arrive under
/// one identifier.
pub fn client_id(hostname: &str) -> String {
    match hostname.trim() {
        "" => "media-browser".to_string(),
        host => format!("media-browser-{host}"),
    }
}

/// The name this machine answers to. In a pod it is the pod's own name, which
/// is unique in the cluster.
pub fn hostname() -> String {
    std::fs::read_to_string("/etc/hostname").unwrap_or_default()
}

/// The broker's host and port. An address with no port answers on the
/// MQTT default, and an empty address is no broker at all. An IPv6 literal
/// carries colons of its own, so the port follows the brackets the URI form
/// puts around the address.
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
