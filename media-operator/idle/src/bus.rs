//! The client's connection to the broker, and the decoding of what arrives on
//! it.
//!
//! Everything this screen draws is a bus fact or a sidecar decision, so this
//! client subscribes to three topics and needs no socket. The status
//! and the volume topics are retained `Player` state the operator publishes.
//! The screen topic carries the idle sidecar's own decisions: the shade
//! travels retained, and the moments do not.
//!
//! The reader runs on a thread of its own and delivers into a channel the
//! screen drains on every wake of its loop, so a broker that answers slowly
//! never delays a frame.

pub mod screen;
pub mod status;
pub mod volume;

use std::sync::mpsc;
use std::time::Duration;

// `rumqttc`'s client type is imported under the broker's name, because this
// crate holds a `Client` of its own, the screen in `client.rs`, and one file
// must not read as if it spoke about both.
use rumqttc::{
    Client as Broker, ConnectionError, Event, MqttOptions, Packet, QoS, SubscribeFilter,
};

use crate::wiring::Topics;
use status::Status;
use volume::Volume;

/// The port a broker answers on when the address names none.
const DEFAULT_PORT: u16 = 1883;

/// The keepalive the client asks the broker for. It is the interval
/// `media-operator`'s own bus client asks for, so every client of one broker
/// keeps the same clock.
const KEEPALIVE: Duration = Duration::from_secs(30);

/// The wait after a failed session. A broker that is down must not become a
/// tight reconnect loop.
const RECONNECT_WAIT: Duration = Duration::from_secs(1);

/// The capacity of `rumqttc`'s outbound request queue, which carries this
/// client's own subscribes. The inbound path to the screen is an unbounded
/// channel, and it drops nothing.
const QUEUE_DEPTH: usize = 64;

/// One message the client read off the bus.
#[derive(Debug, Clone, PartialEq)]
pub enum Message {
    /// The unit's whole presentable state.
    Status(Status),
    /// The unit's listening level. `pressed` is false for the broker's
    /// catch-up and true for a press.
    Level { volume: Volume, pressed: bool },
    /// One moment the idle sidecar decided.
    Screen(screen::Event),
}

/// The decoding of one bus session: which topic carries what.
///
/// No socket reaches this type, so a test proves the rules below without a
/// broker.
#[derive(Debug)]
pub struct Session {
    topics: Topics,
}

impl Session {
    pub fn new(topics: Topics) -> Self {
        Self { topics }
    }

    /// The topics to subscribe to. An empty topic is one the operator did not
    /// set, and a unit with no sinks has no volume topic at all.
    pub fn filters(&self) -> Vec<String> {
        [
            &self.topics.status,
            &self.topics.volume,
            &self.topics.screen,
        ]
        .into_iter()
        .filter(|topic| !topic.is_empty())
        .cloned()
        .collect()
    }

    /// One inbound message, decoded. A topic this session did not subscribe to,
    /// and a payload that does not decode, are both nothing at all.
    ///
    /// `retained` is the broker's own mark on a delivery from its retained
    /// store. A retained level is the catch-up, which a person did not press,
    /// so it sets the level and shows no indicator; a live level is a press.
    /// The message says which it is, so the session holds no state, and a
    /// reconnect never swallows a press.
    pub fn deliver(&mut self, topic: &str, payload: &[u8], retained: bool) -> Option<Message> {
        if !self.topics.status.is_empty() && topic == self.topics.status {
            return status::parse(payload).map(Message::Status);
        }
        if !self.topics.volume.is_empty() && topic == self.topics.volume {
            let volume = volume::parse(payload)?;
            return Some(Message::Level {
                volume,
                pressed: !retained,
            });
        }
        if !self.topics.screen.is_empty() && topic == self.topics.screen {
            return screen::parse(payload).map(Message::Screen);
        }
        None
    }
}

/// The subscription, held by the screen. Dropping it closes the channel, and
/// the reader thread ends on its next delivery.
#[derive(Debug)]
pub struct Reader {
    messages: mpsc::Receiver<Message>,
}

impl Reader {
    /// Connect to the broker at `address` and subscribe. The answer is `None`
    /// when the operator named no broker or no topics, which is how the client
    /// runs on a workstation with the seeds alone.
    ///
    /// `client_id` must name this client alone. The idle pod holds a second bus
    /// client in its sidecar, and a broker closes the older connection when two
    /// arrive under one identifier.
    pub fn open(address: &str, client_id: &str, topics: Topics) -> Option<Self> {
        let session = Session::new(topics);
        let (host, port) = broker(address)?;
        if session.filters().is_empty() {
            return None;
        }

        let mut options = MqttOptions::new(client_id, host, port);
        options.set_keep_alive(KEEPALIVE);
        let (client, connection) = Broker::new(options, QUEUE_DEPTH);

        let (sender, messages) = mpsc::channel();
        std::thread::Builder::new()
            .name("media-bus".into())
            .spawn(move || read(session, client, connection, &sender))
            // A client that spawns no reader draws the seeds and hears
            // nothing for the life of the pod, so the line says why.
            .inspect_err(|error| eprintln!("idle-screen: bus: {error}"))
            .ok()?;

        Some(Self { messages })
    }

    /// Every message that arrived since the last call. The call never blocks,
    /// so the wake it runs in costs the decoding and nothing else.
    pub fn drain(&self) -> Vec<Message> {
        self.messages.try_iter().collect()
    }

    /// A reader over an open channel, standing in for the thread, so a test
    /// proves the screen's side of the seam without a broker.
    #[cfg(test)]
    pub(crate) fn from_channel(messages: mpsc::Receiver<Message>) -> Self {
        Self { messages }
    }
}

/// The reader thread. It subscribes on every connection, because a broker holds
/// no subscription across a session, and it decodes each message before the
/// channel so the frame loop takes finished values.
fn read(
    mut session: Session,
    client: Broker,
    mut connection: rumqttc::Connection,
    sender: &mpsc::Sender<Message>,
) {
    for event in connection.iter() {
        match event {
            Ok(Event::Incoming(Packet::ConnAck(_))) => {
                let filters = session.filters().into_iter().map(|path| SubscribeFilter {
                    path,
                    qos: QoS::AtMostOnce,
                });
                // The subscribe is queued rather than sent, because this thread
                // is the one that drives the connection. A blocking send would
                // wait for a reader that is this loop.
                let _ = client.try_subscribe_many(filters);
            }
            Ok(Event::Incoming(Packet::Publish(publish))) => {
                if let Some(message) =
                    session.deliver(&publish.topic, &publish.payload, publish.retain)
                    && sender.send(message).is_err()
                {
                    // The screen dropped its reader, so nothing reads what this
                    // thread decodes.
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

/// One line for a failed session. The client reconnects on its own, so the line
/// is the record and not a request for anything.
fn report(error: &ConnectionError) {
    eprintln!("idle-screen: bus: {error}");
}

/// Read one JSON object off a topic. Every payload on these three topics is an
/// object, so a payload that is not one is not this operator's and it changes
/// nothing. The check is here because a derived reader also takes a JSON array
/// as the same fields in order, and a two-element array is not a status.
pub(crate) fn object<T: serde::de::DeserializeOwned>(payload: &[u8]) -> Option<T> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    if !value.is_object() {
        return None;
    }
    serde_json::from_value(value).ok()
}

/// The identifier this client connects under. It must name this client alone,
/// because a broker closes the older connection when two arrive under one
/// identifier, and the idle pod holds a second bus client in its sidecar.
pub fn client_id(hostname: &str) -> String {
    match hostname.trim() {
        "" => "idle-screen".to_string(),
        host => format!("idle-screen-{host}"),
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
/// brackets the URI form puts around the address, and a bare literal is
/// the host alone on the default port.
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
mod tests {
    use super::*;
    use status::Activity;

    const STATUS: &str = "media/players/den/tv/status";
    const VOLUME: &str = "media/players/den/tv/volume";
    const SCREEN: &str = "media/players/den/tv/screen";

    fn session() -> Session {
        Session::new(Topics {
            status: STATUS.into(),
            volume: VOLUME.into(),
            screen: SCREEN.into(),
        })
    }

    #[test]
    fn the_session_subscribes_to_the_topics_the_operator_named() {
        assert_eq!(session().filters(), [STATUS, VOLUME, SCREEN]);
    }

    #[test]
    fn a_unit_with_no_sinks_subscribes_to_no_level() {
        let session = Session::new(Topics {
            status: STATUS.into(),
            volume: String::new(),
            screen: SCREEN.into(),
        });
        assert_eq!(session.filters(), [STATUS, SCREEN]);
    }

    #[test]
    fn a_status_decodes_off_its_own_topic() {
        let message = session().deliver(
            STATUS,
            br#"{"displayName":"The Den","activity":"Playing"}"#,
            true,
        );
        let Some(Message::Status(status)) = message else {
            panic!("the status decodes");
        };
        assert_eq!(status.display_name, "The Den");
        assert_eq!(status.activity, Activity::Playing);
    }

    #[test]
    fn a_moment_decodes_off_the_screen_topic() {
        assert_eq!(
            session().deliver(SCREEN, br#"{"event":"present"}"#, false),
            Some(Message::Screen(screen::Event::Present))
        );
    }

    #[test]
    fn a_retained_level_is_the_catch_up_and_a_live_level_is_a_press() {
        let mut session = session();

        assert_eq!(
            session.deliver(VOLUME, br#"{"level":40,"muted":false}"#, true),
            Some(Message::Level {
                volume: Volume {
                    level: 40,
                    muted: false
                },
                pressed: false,
            })
        );
        assert_eq!(
            session.deliver(VOLUME, br#"{"level":45,"muted":false}"#, false),
            Some(Message::Level {
                volume: Volume {
                    level: 45,
                    muted: false
                },
                pressed: true,
            })
        );
    }

    #[test]
    fn a_level_that_does_not_parse_is_nothing() {
        assert_eq!(session().deliver(VOLUME, b"loud", false), None);
    }

    #[test]
    fn a_topic_this_session_did_not_subscribe_to_is_nothing() {
        assert_eq!(
            session().deliver("media/players/den/tv/commands", b"{}", false),
            None
        );
        let mut deaf = Session::new(Topics::default());
        assert_eq!(deaf.deliver("", b"{}", false), None);
    }

    #[test]
    fn text_that_does_not_parse_changes_nothing() {
        let mut session = session();
        assert_eq!(session.deliver(STATUS, b"{", true), None);
        assert_eq!(session.deliver(SCREEN, b"{", false), None);
    }

    #[test]
    fn an_address_with_no_port_answers_on_the_mqtt_default() {
        assert_eq!(broker("broker"), Some(("broker".into(), 1883)));
        assert_eq!(broker(" broker:1884 "), Some(("broker".into(), 1884)));
    }

    #[test]
    fn an_address_this_client_cannot_read_is_no_broker() {
        assert_eq!(broker(""), None);
        assert_eq!(broker("broker:soon"), None);
        assert_eq!(broker("[::1"), None);
        assert_eq!(broker("[::1]:soon"), None);
        assert_eq!(broker("[::1]1883"), None);
    }

    #[test]
    fn an_ipv6_literal_reads_its_port_off_the_brackets() {
        assert_eq!(broker("[::1]:1884"), Some(("::1".into(), 1884)));
        assert_eq!(broker(" [fd00::1]:1884 "), Some(("fd00::1".into(), 1884)));
        assert_eq!(broker("[::1]"), Some(("::1".into(), 1883)));
    }

    #[test]
    fn an_ipv6_literal_with_no_brackets_answers_on_the_mqtt_default() {
        assert_eq!(broker("::1"), Some(("::1".into(), 1883)));
        assert_eq!(broker("fd00::1"), Some(("fd00::1".into(), 1883)));
    }

    #[test]
    fn the_client_identifier_names_the_machine_it_runs_on() {
        assert_eq!(
            client_id("den-tv-idle-7f4\n"),
            "idle-screen-den-tv-idle-7f4"
        );
        assert_eq!(client_id(""), "idle-screen");
    }

    #[test]
    fn a_client_with_no_broker_and_a_client_with_no_topics_open_no_reader() {
        assert!(Reader::open("", "idle-screen", Topics::default()).is_none());
        assert!(Reader::open("127.0.0.1:1883", "idle-screen", Topics::default()).is_none());
    }
}
