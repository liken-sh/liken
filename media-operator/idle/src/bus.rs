//! The client's connection to the broker, and the decoding of what arrives on
//! it.
//!
//! Everything this screen draws is a bus fact or a sidecar decision, so this
//! client subscribes to three topics and needs no socket. The status
//! and the volume topics are retained `Player` state the operator publishes.
//! The screen topic carries the idle sidecar's own decisions and retains
//! nothing.
//!
//! The reader runs on a thread of its own and delivers into a channel the
//! screen drains once a frame, so a broker that answers slowly never delays a
//! frame.

pub mod screen;
pub mod status;
pub mod volume;

use std::sync::mpsc;
use std::time::Duration;

use rumqttc::{Client, ConnectionError, Event, MqttOptions, Packet, QoS, SubscribeFilter};

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

/// How many messages the channel holds before the reader drops one. The screen
/// drains the channel every frame, so the depth only covers a burst that lands
/// between two frames.
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

/// The decoding of one bus session: which topic carries what, and the one piece
/// of state that separates a catch-up from a press.
///
/// No socket reaches this type, so a test proves the rules below without a
/// broker.
#[derive(Debug)]
pub struct Session {
    topics: Topics,
    /// Whether this session has already delivered a level. The first message
    /// of a session is the broker's retained catch-up, which a person did not
    /// press, so it sets the level and shows no indicator. Every message after
    /// it is a press. `media-operator`'s idle sidecar holds the same rule in
    /// its `volumeCaughtUp` field, for the same topic.
    caught_up: bool,
}

impl Session {
    pub fn new(topics: Topics) -> Self {
        Self {
            topics,
            caught_up: false,
        }
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

    /// A new session began. The broker redelivers the retained level on every
    /// session, so that message is a catch-up again and shows no indicator
    /// again.
    pub fn opened(&mut self) {
        self.caught_up = false;
    }

    /// One inbound message, decoded. A topic this session did not subscribe to,
    /// and a payload that does not decode, are both nothing at all.
    pub fn deliver(&mut self, topic: &str, payload: &[u8]) -> Option<Message> {
        if !self.topics.status.is_empty() && topic == self.topics.status {
            return status::parse(payload).map(Message::Status);
        }
        if !self.topics.volume.is_empty() && topic == self.topics.volume {
            let volume = volume::parse(payload)?;
            let pressed = self.caught_up;
            self.caught_up = true;
            return Some(Message::Level { volume, pressed });
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
        let (client, connection) = Client::new(options, QUEUE_DEPTH);

        let (sender, messages) = mpsc::channel();
        std::thread::Builder::new()
            .name("media-bus".into())
            .spawn(move || read(session, client, connection, &sender))
            .ok()?;

        Some(Self { messages })
    }

    /// Every message that arrived since the last call. The call never blocks,
    /// so the frame it runs in costs the decoding and nothing else.
    pub fn drain(&self) -> Vec<Message> {
        self.messages.try_iter().collect()
    }
}

/// The reader thread. It subscribes on every connection, because a broker holds
/// no subscription across a session, and it decodes each message before the
/// channel so the frame loop takes finished values.
fn read(
    mut session: Session,
    client: Client,
    mut connection: rumqttc::Connection,
    sender: &mpsc::Sender<Message>,
) {
    for event in connection.iter() {
        match event {
            Ok(Event::Incoming(Packet::ConnAck(_))) => {
                session.opened();
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
                if let Some(message) = session.deliver(&publish.topic, &publish.payload)
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
fn broker(address: &str) -> Option<(String, u16)> {
    let address = address.trim();
    if address.is_empty() {
        return None;
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
        let message =
            session().deliver(STATUS, br#"{"displayName":"The Den","activity":"Playing"}"#);
        let Some(Message::Status(status)) = message else {
            panic!("the status decodes");
        };
        assert_eq!(status.display_name, "The Den");
        assert_eq!(status.activity, Activity::Playing);
    }

    #[test]
    fn a_moment_decodes_off_the_screen_topic() {
        assert_eq!(
            session().deliver(SCREEN, br#"{"event":"present"}"#),
            Some(Message::Screen(screen::Event::Present))
        );
    }

    #[test]
    fn the_first_level_of_a_session_is_the_catch_up_and_every_one_after_it_is_a_press() {
        let mut session = session();

        assert_eq!(
            session.deliver(VOLUME, br#"{"level":40,"muted":false}"#),
            Some(Message::Level {
                volume: Volume {
                    level: 40,
                    muted: false
                },
                pressed: false,
            })
        );
        assert_eq!(
            session.deliver(VOLUME, br#"{"level":45,"muted":false}"#),
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
    fn a_new_session_reads_its_first_level_as_a_catch_up_again() {
        let mut session = session();
        session.deliver(VOLUME, br#"{"level":40,"muted":false}"#);
        session.deliver(VOLUME, br#"{"level":45,"muted":false}"#);

        session.opened();

        assert_eq!(
            session.deliver(VOLUME, br#"{"level":45,"muted":false}"#),
            Some(Message::Level {
                volume: Volume {
                    level: 45,
                    muted: false
                },
                pressed: false,
            })
        );
    }

    #[test]
    fn a_level_that_does_not_parse_leaves_the_catch_up_unspent() {
        let mut session = session();
        assert_eq!(session.deliver(VOLUME, b"loud"), None);
        assert_eq!(
            session.deliver(VOLUME, br#"{"level":40,"muted":false}"#),
            Some(Message::Level {
                volume: Volume {
                    level: 40,
                    muted: false
                },
                pressed: false,
            })
        );
    }

    #[test]
    fn a_topic_this_session_did_not_subscribe_to_is_nothing() {
        assert_eq!(
            session().deliver("media/players/den/tv/commands", b"{}"),
            None
        );
        let mut deaf = Session::new(Topics::default());
        assert_eq!(deaf.deliver("", b"{}"), None);
    }

    #[test]
    fn text_that_does_not_parse_changes_nothing() {
        let mut session = session();
        assert_eq!(session.deliver(STATUS, b"{"), None);
        assert_eq!(session.deliver(SCREEN, b"{"), None);
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
