// Most tests here use no socket. `rumqttc`'s `Client::new` opens nothing:
// it creates the request queue and returns a connection that the caller
// drives. A test builds a client, passes `read` a fixed list of the events
// a broker would send, and checks what reached the channel.

use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::atomic::{AtomicUsize, Ordering};

use metrics::with_local_recorder;
use metrics_util::debugging::{DebugValue, DebuggingRecorder, Snapshotter};
use rumqttc::{ConnAck, ConnectReturnCode, Publish as Message};

use super::*;
use crate::Bus;
use crate::wiring::Remote;

/// The tests that call this install a recorder for the current thread only,
/// so the gauge that one test sets does not appear in another.
fn gauge(snapshotter: &Snapshotter, name: &str) -> Option<f64> {
    snapshotter
        .snapshot()
        .into_vec()
        .into_iter()
        .find_map(
            |(key, _, _, value)| match (key.key().name() == name, value) {
                (true, DebugValue::Gauge(value)) => Some(value.into_inner()),
                _ => None,
            },
        )
}

const STATUS: &str = "liken/media/players/default/player-1/status";
const PANEL: &str = "liken/media/players/default/player-1/panel";
const EVENTS: &str = "liken/media/remotes/default/remote-1/events";
const FOCUS: &str = "liken/media/remotes/default/remote-1/focus";

fn wiring() -> Wiring {
    Wiring {
        bus_address: "127.0.0.1:1883".into(),
        player_name: "player-1".into(),
        status_topic: STATUS.into(),
        panel_topic: PANEL.into(),
        remotes: vec![Remote {
            events: EVENTS.into(),
            focus: FOCUS.into(),
        }],
        ..Wiring::default()
    }
}

/// A [`Reader`] and its [`Threads`], set up as [`Reader::open`] sets them up
/// but over a client with no socket, so every publish and subscribe stays in
/// the request queue.
fn reader() -> (Reader, Threads) {
    reader_over(wiring())
}

/// For tests that need a short timer.
fn reader_over(wiring: Wiring) -> (Reader, Threads) {
    reader_reading(wiring, &[])
}

fn reader_reading(wiring: Wiring, client_topics: &[String]) -> (Reader, Threads) {
    let (client, _) = Broker::new(MqttOptions::new("test", "localhost", 1883), QUEUE_DEPTH);
    let (sender, moments) = mpsc::channel();
    let screen = Arc::new(Mutex::new(Screen::new(&wiring).reading(client_topics)));
    let threads = Threads {
        screen: Arc::downgrade(&screen),
        client,
        sender,
        waker: Arc::default(),
        alarm: Arc::default(),
    };
    (
        Reader {
            moments,
            screen,
            threads: threads.clone(),
        },
        threads,
    )
}

fn deliver_idle_status(reader: &Reader) {
    reader
        .screen
        .lock()
        .expect("the screen lock is poisoned")
        .deliver(STATUS, br#"{"activity":"Idle"}"#, true, Instant::now());
}

fn counting_waker(threads: &Threads) -> Arc<AtomicUsize> {
    let woken = Arc::new(AtomicUsize::new(0));
    let counter = Arc::clone(&woken);
    let wake: Waker = Arc::new(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    });
    *threads.waker.lock().expect("the waker lock is poisoned") = Some(wake);
    woken
}

fn incoming_publish(topic: &str, payload: &str, retain: bool) -> Event {
    let mut message = Message::new(topic, QoS::AtMostOnce, payload.as_bytes().to_vec());
    message.retain = retain;
    Event::Incoming(Packet::Publish(message))
}

fn connack() -> Event {
    Event::Incoming(Packet::ConnAck(ConnAck {
        session_present: false,
        code: ConnectReturnCode::Success,
    }))
}

#[test]
fn a_client_with_no_broker_and_a_client_with_no_topics_open_no_reader() {
    assert!(
        Reader::open(
            &Wiring {
                bus_address: String::new(),
                ..wiring()
            },
            "idle-screen",
            &[]
        )
        .is_none()
    );
    assert!(
        Reader::open(
            &Wiring {
                bus_address: "127.0.0.1:1883".into(),
                ..Wiring::default()
            },
            "idle-screen",
            &[]
        )
        .is_none()
    );
}

#[test]
fn a_client_with_only_topics_of_its_own_opens_a_reader() {
    // The wiring has no topics, so the client's own topic is the whole
    // subscription. Nothing listens on port 1, so the session fails.
    let reader = Reader::open(
        &Wiring {
            bus_address: "127.0.0.1:1".into(),
            ..Wiring::default()
        },
        "media-screen-test",
        &["liken/library/screens/default/player-1/watching".into()],
    );

    assert!(reader.is_some());
}

#[test]
fn a_new_session_sends_the_client_a_connected_moment() {
    let (reader, threads) = reader();

    read(&threads, [Ok(connack())].into_iter());

    // The panel request is a publish, so it goes to the request queue and
    // not to the client.
    assert_eq!(reader.drain(), [Moment::Connected]);
}

#[test]
fn opening_a_reader_marks_the_bus_not_yet_connected() {
    let recorder = DebuggingRecorder::new();
    let snapshotter = recorder.snapshotter();

    // `Reader::open` sets the gauge before it starts either thread, so the
    // result does not depend on the failed sessions to port 1.
    let reader = with_local_recorder(&recorder, || {
        Reader::open(
            &Wiring {
                bus_address: "127.0.0.1:1".into(),
                ..wiring()
            },
            "media-screen-test",
            &[],
        )
    });

    assert!(reader.is_some());
    assert_eq!(gauge(&snapshotter, "media_bus_connected"), Some(0.0));
}

#[test]
fn a_session_marks_the_bus_connected() {
    let (_reader, threads) = reader();
    let recorder = DebuggingRecorder::new();
    let snapshotter = recorder.snapshotter();

    with_local_recorder(&recorder, || {
        read(&threads, [Ok(connack())].into_iter());
    });

    assert_eq!(gauge(&snapshotter, "media_bus_connected"), Some(1.0));
}

#[test]
fn a_failed_session_marks_the_bus_not_connected() {
    let (_reader, threads) = reader();
    let recorder = DebuggingRecorder::new();
    let snapshotter = recorder.snapshotter();

    with_local_recorder(&recorder, || {
        read(&threads, [Err(ConnectionError::RequestsDone)].into_iter());
    });

    assert_eq!(gauge(&snapshotter, "media_bus_connected"), Some(0.0));
}

#[test]
fn a_message_on_a_client_topic_reaches_the_client() {
    let owned = "liken/library/screens/default/player-1/watching";
    let (reader, threads) = reader_reading(wiring(), &[owned.into()]);

    read(
        &threads,
        [Ok(incoming_publish(owned, "person-a", true))].into_iter(),
    );

    assert_eq!(
        reader.drain(),
        [Moment::Message {
            topic: owned.into(),
            payload: b"person-a".to_vec(),
            retained: true,
        }]
    );
}

#[test]
fn a_delivery_reaches_the_client_and_wakes_the_loop_once() {
    let (reader, threads) = reader();
    let woken = counting_waker(&threads);

    read(
        &threads,
        [Ok(incoming_publish(
            STATUS,
            r#"{"displayName":"Player One","activity":"Idle"}"#,
            true,
        ))]
        .into_iter(),
    );

    assert_eq!(reader.drain().len(), 1);
    assert_eq!(woken.load(Ordering::SeqCst), 1);
}

#[test]
fn a_message_that_produces_no_moment_does_not_wake_the_loop() {
    let (reader, threads) = reader();
    let woken = counting_waker(&threads);

    // A retained message on the focus topic changes state and produces no
    // moment.
    read(
        &threads,
        [Ok(incoming_publish(FOCUS, "player-1", true))].into_iter(),
    );

    assert!(reader.drain().is_empty());
    assert_eq!(woken.load(Ordering::SeqCst), 0);
}

#[test]
fn a_payload_that_is_not_json_sends_no_moment() {
    let (reader, threads) = reader();

    read(
        &threads,
        [Ok(incoming_publish(EVENTS, "not json", false))].into_iter(),
    );

    assert!(reader.drain().is_empty());
}

#[test]
fn a_failed_session_does_not_stop_the_reader_loop() {
    let (reader, threads) = reader();

    read(
        &threads,
        [
            Err(ConnectionError::RequestsDone),
            Ok(incoming_publish(
                STATUS,
                r#"{"displayName":"Player One","activity":"Idle"}"#,
                true,
            )),
        ]
        .into_iter(),
    );

    assert_eq!(reader.drain().len(), 1);
}

#[test]
fn an_event_the_reader_ignores_produces_no_moment() {
    let (reader, threads) = reader();

    read(
        &threads,
        [Ok(Event::Incoming(Packet::PingResp))].into_iter(),
    );

    assert!(reader.drain().is_empty());
}

#[test]
fn a_delivery_after_the_client_dropped_its_reader_ends_the_thread() {
    let (reader, threads) = reader();
    drop(reader);

    // The rules are gone, so the first event ends the loop.
    read(
        &threads,
        [
            Ok(incoming_publish(STATUS, r#"{"activity":"Idle"}"#, true)),
            Ok(incoming_publish(STATUS, r#"{"activity":"Playing"}"#, true)),
        ]
        .into_iter(),
    );

    assert!(threads.screen().is_none());
}

#[test]
fn a_client_that_dropped_its_channel_ends_the_thread() {
    let (mut reader, threads) = reader();
    let (sender, closed) = mpsc::channel();
    reader.moments = closed;
    drop(sender);

    read(
        &threads,
        [Ok(incoming_publish(STATUS, r#"{"activity":"Idle"}"#, true))].into_iter(),
    );

    assert!(reader.drain().is_empty());
}

#[test]
fn sleep_sends_a_sleep_moment_back_to_the_client() {
    let (reader, _threads) = reader();
    deliver_idle_status(&reader);

    reader.sleep();

    assert_eq!(reader.drain(), [Moment::Sleep]);
}

#[test]
fn the_reconnect_wait_doubles_up_to_the_ceiling() {
    let waits: Vec<Duration> =
        std::iter::successors(Some(RECONNECT_MIN), |wait| Some(next_backoff(*wait)))
            .take(7)
            .collect();

    assert_eq!(waits, [1, 2, 4, 8, 16, 30, 30].map(Duration::from_secs));
}

fn spawn_clock(threads: &Threads) -> std::thread::JoinHandle<()> {
    let running = threads.clone();
    std::thread::spawn(move || clock(&running))
}

/// Polls until no timer is armed or `limit` passes, and returns the elapsed
/// time. The limit makes a clock that never fires fail the test instead of
/// hanging it.
fn time_until_disarmed(reader: &Reader, limit: Duration) -> Duration {
    let start = Instant::now();
    while start.elapsed() < limit
        && reader
            .screen
            .lock()
            .expect("the screen lock is poisoned")
            .next_deadline()
            .is_some()
    {
        std::thread::sleep(Duration::from_millis(5));
    }
    start.elapsed()
}

#[test]
fn a_timer_armed_by_another_thread_ends_the_clocks_wait() {
    // The fade timer is an hour, so the clock starts waiting for an hour.
    // The client's `sleep` call then arms the off timer 50 ms out, and only
    // a signal of the alarm can end the clock's wait that soon.
    let (reader, threads) = reader_over(Wiring {
        fade_after: Duration::from_secs(3600),
        off_after: Duration::from_secs(3600) + Duration::from_millis(50),
        ..wiring()
    });
    deliver_idle_status(&reader);
    let thread = spawn_clock(&threads);
    std::thread::sleep(Duration::from_millis(50));

    reader.sleep();

    // The 500 ms limit is well above normal scheduling delay and well below
    // the hour that the clock started waiting for.
    assert!(time_until_disarmed(&reader, Duration::from_secs(2)) < Duration::from_millis(500));
    drop(reader);
    thread.join().expect("the clock thread panicked");
}

#[test]
fn a_clock_with_nothing_armed_ends_when_the_client_drops_its_reader() {
    // No status has arrived, so no timer is armed and the clock waits with
    // no timeout until the reader is dropped.
    let (reader, threads) = reader();
    let (ended, ends) = mpsc::channel();
    let running = threads.clone();
    std::thread::spawn(move || {
        clock(&running);
        ended
            .send(())
            .expect("the test stopped waiting for the clock to end");
    });
    std::thread::sleep(Duration::from_millis(50));

    drop(reader);

    assert_eq!(ends.recv_timeout(Duration::from_secs(2)), Ok(()));
}

#[test]
fn the_clock_fires_a_timer_and_ends_when_the_client_drops_its_reader() {
    // The fade timer is short, so the clock fires it at once and the client
    // receives the sleep moment.
    let (reader, threads) = reader_over(Wiring {
        fade_after: Duration::from_millis(5),
        ..wiring()
    });
    deliver_idle_status(&reader);

    let thread = spawn_clock(&threads);
    while reader.drain().is_empty() {
        std::thread::sleep(Duration::from_millis(10));
    }

    drop(reader);
    thread.join().expect("the clock thread panicked");
}

#[test]
fn a_clock_with_no_client_ends_at_once() {
    let (reader, threads) = reader();
    drop(reader);

    clock(&threads);

    assert!(threads.screen().is_none());
}

#[test]
fn a_clock_whose_client_dropped_its_channel_ends_the_thread() {
    let (mut reader, threads) = reader_over(Wiring {
        fade_after: Duration::from_millis(5),
        ..wiring()
    });
    deliver_idle_status(&reader);
    let (sender, closed) = mpsc::channel();
    reader.moments = closed;
    drop(sender);

    clock(&threads);

    // The fade timer fired, and the clock ended when it could not send the
    // sleep moment.
    assert!(reader.drain().is_empty());
}

#[test]
fn a_debug_line_names_the_reader_and_nothing_inside_it() {
    let (reader, _threads) = reader();

    assert_eq!(format!("{reader:?}"), "Reader { .. }");
}

#[test]
fn the_client_identifier_names_the_client_and_its_pod() {
    assert_eq!(
        client_id("idle-screen", "player-1-idle-7f4\n"),
        "idle-screen-player-1-idle-7f4"
    );
    assert_eq!(client_id("idle-screen", ""), "idle-screen");
}

#[test]
fn hostname_does_not_fail_when_the_file_is_missing() {
    let _ = hostname();
}

#[test]
fn an_address_with_no_port_uses_the_mqtt_default_port() {
    assert_eq!(broker("broker"), Some(("broker".into(), 1883)));
    assert_eq!(broker(" broker:1884 "), Some(("broker".into(), 1884)));
}

#[test]
fn an_unparseable_address_has_no_broker() {
    assert_eq!(broker(""), None);
    assert_eq!(broker("broker:soon"), None);
    assert_eq!(broker("[::1"), None);
    assert_eq!(broker("[::1]:soon"), None);
    assert_eq!(broker("[::1]1883"), None);
}

#[test]
fn an_ipv6_literal_in_brackets_has_its_port_after_the_bracket() {
    assert_eq!(broker("[::1]:1884"), Some(("::1".into(), 1884)));
    assert_eq!(broker(" [fd00::1]:1884 "), Some(("fd00::1".into(), 1884)));
    assert_eq!(broker("[::1]"), Some(("::1".into(), 1883)));
}

#[test]
fn an_ipv6_literal_with_no_brackets_uses_the_mqtt_default_port() {
    assert_eq!(broker("::1"), Some(("::1".into(), 1883)));
    assert_eq!(broker("fd00::1"), Some(("fd00::1".into(), 1883)));
}

#[test]
fn a_reader_accepts_a_waker_and_ends_with_the_client() {
    // Nothing listens on port 1, so the reader thread retries as it does
    // while the broker is down.
    let reader = Reader::open(
        &Wiring {
            bus_address: "127.0.0.1:1".into(),
            ..wiring()
        },
        "media-screen-test",
        &[],
    )
    .expect("Reader::open returned None for a wiring with a broker address");

    reader.wake_on_delivery(Arc::new(|| {}));

    assert!(reader.drain().is_empty());
}

/// A fake broker for one client. It responds to the CONNECT with a CONNACK,
/// then reads without parsing, because the tests only check that certain
/// bytes reach the connection. Its thread returns true when `wanted`
/// appears in what the client wrote, or false after 5 seconds.
fn fake_broker(wanted: Vec<u8>) -> (u16, std::thread::JoinHandle<bool>) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("binding the fake broker to a port");
    let port = listener
        .local_addr()
        .expect("reading the fake broker port")
        .port();

    let thread = std::thread::spawn(move || {
        let (mut client, _) = listener.accept().expect("accepting the client connection");
        client
            .set_read_timeout(Some(Duration::from_millis(100)))
            .expect("setting the read timeout");
        // The client sends its queued packets only after the CONNACK.
        let _ = client.read(&mut [0_u8; 1024]);
        client
            .write_all(&[0x20, 0x02, 0x00, 0x00])
            .expect("writing the CONNACK");

        let deadline = Instant::now() + Duration::from_secs(5);
        let mut written: Vec<u8> = Vec::new();
        while Instant::now() < deadline {
            let mut buffer = [0_u8; 1024];
            if let Ok(read) = client.read(&mut buffer) {
                if read == 0 {
                    break;
                }
                written.extend_from_slice(&buffer[..read]);
                if written.windows(wanted.len()).any(|run| run == wanted) {
                    return true;
                }
            }
        }
        false
    });

    (port, thread)
}

/// One topic filter of a SUBSCRIBE packet: the two-byte length, the topic,
/// and QoS 0, which this crate requests for every filter.
fn subscribe_filter(topic: &str) -> Vec<u8> {
    let mut wire = Vec::from((topic.len() as u16).to_be_bytes());
    wire.extend(topic.as_bytes());
    wire.push(0x00);
    wire
}

#[test]
fn a_session_subscribes_to_the_clients_own_topics() {
    let owned = "liken/library/screens/default/player-1/watching";
    let (port, broker) = fake_broker(subscribe_filter(owned));

    let reader = Reader::open(
        &Wiring {
            bus_address: format!("127.0.0.1:{port}"),
            ..wiring()
        },
        "media-screen-test",
        &[owned.into()],
    )
    .expect("Reader::open returned None for a wiring with a broker address");

    assert!(
        broker.join().expect("the fake broker thread panicked"),
        "the broker did not receive a subscribe to the client's own topic"
    );
    drop(reader);
}

/// One QoS 0 PUBLISH packet, with a remaining length under 128, which MQTT
/// encodes in one byte.
fn publish_packet(topic: &str, payload: &[u8], retained: bool) -> Vec<u8> {
    let length = 2 + topic.len() + payload.len();
    assert!(
        length < 128,
        "the remaining length is {length}, and this helper writes one byte, so it must be under 128"
    );

    let mut wire = vec![0x30 | u8::from(retained), length as u8];
    wire.extend((topic.len() as u16).to_be_bytes());
    wire.extend(topic.as_bytes());
    wire.extend(payload);
    wire
}

#[test]
fn a_clients_own_publish_goes_out_on_the_readers_connection() {
    let topic = "liken/library/plays/default/requests";
    let payload = br#"{"list":"friday"}"#.to_vec();
    let (port, broker) = fake_broker(publish_packet(topic, &payload, true));

    let reader = Reader::open(
        &Wiring {
            bus_address: format!("127.0.0.1:{port}"),
            ..wiring()
        },
        "media-screen-test",
        &[],
    )
    .expect("Reader::open returned None for a wiring with a broker address");

    reader.publish(topic, payload, true);

    assert!(
        broker.join().expect("the fake broker thread panicked"),
        "the broker did not receive the publish on the reader's connection"
    );
    // The client passed the reader no topics, so its publish does not come
    // back to it.
    assert!(
        reader
            .drain()
            .iter()
            .all(|moment| *moment == Moment::Connected)
    );
}
