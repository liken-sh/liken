// The socket half, proved with no socket. `rumqttc`'s `Client::new` opens
// nothing: it makes the request queue and hands back a connection the caller
// drives. So a test builds a client, feeds the reader a canned sequence of
// the events a broker would send, and reads what reached the channel.

use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::atomic::{AtomicUsize, Ordering};

use rumqttc::{ConnAck, ConnectReturnCode, Publish as Message};

use super::*;
use crate::Bus;
use crate::wiring::Remote;

const STATUS: &str = "liken/media/players/house/theater/status";
const PANEL: &str = "liken/media/players/house/theater/panel";
const EVENTS: &str = "liken/media/remotes/house/sofa/events";
const FOCUS: &str = "liken/media/remotes/house/sofa/focus";

fn wiring() -> Wiring {
    Wiring {
        bus_address: "127.0.0.1:1883".into(),
        player_name: "theater".into(),
        status_topic: STATUS.into(),
        panel_topic: PANEL.into(),
        remotes: vec![Remote {
            events: EVENTS.into(),
            focus: FOCUS.into(),
        }],
        ..Wiring::default()
    }
}

/// One reader over a broker that connects to nothing, and the threads' half
/// of it. `rumqttc`'s `Client::new` opens no socket, so every publish and
/// every subscribe lands in the queue and goes no further.
///
/// The reader holds the rules and the threads hold a weak reference, the way
/// [`Reader::open`] builds them, so a test that drops the reader ends the
/// threads.
fn reader() -> (Reader, Threads) {
    reader_over(wiring())
}

/// The same reader over a wiring a test states, for the one test that needs a
/// window short enough to run inside it.
fn reader_over(wiring: Wiring) -> (Reader, Threads) {
    let (client, _) = Broker::new(MqttOptions::new("test", "localhost", 1883), QUEUE_DEPTH);
    let (sender, moments) = mpsc::channel();
    let screen = Arc::new(Mutex::new(Screen::new(&wiring)));
    let threads = Threads {
        screen: Arc::downgrade(&screen),
        client,
        sender,
        waker: Arc::default(),
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

/// The unit's retained status, already read, so the screen plays nothing.
fn idling(reader: &Reader) {
    reader.screen.lock().expect("the lock is free").deliver(
        STATUS,
        br#"{"activity":"Idle"}"#,
        true,
        Instant::now(),
    );
}

/// A waker that counts its calls, so a test reads whether one fold woke the
/// loop once or not at all.
fn counting(threads: &Threads) -> Arc<AtomicUsize> {
    let woken = Arc::new(AtomicUsize::new(0));
    let counter = Arc::clone(&woken);
    let wake: Waker = Arc::new(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    });
    *threads.waker.lock().expect("the lock is free") = Some(wake);
    woken
}

/// One message off the broker, retained or live.
fn publish(topic: &str, payload: &str, retain: bool) -> Event {
    let mut message = Message::new(topic, QoS::AtMostOnce, payload.as_bytes().to_vec());
    message.retain = retain;
    Event::Incoming(Packet::Publish(message))
}

/// The acknowledgement that starts a session.
fn connected() -> Event {
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
            "idle-screen"
        )
        .is_none()
    );
    assert!(
        Reader::open(
            &Wiring {
                bus_address: "127.0.0.1:1883".into(),
                ..Wiring::default()
            },
            "idle-screen"
        )
        .is_none()
    );
}

#[test]
fn a_session_subscribes_and_states_the_panel_desire() {
    let (reader, threads) = reader();

    read(&threads, [Ok(connected())].into_iter());

    // The desire is a publish, so it reaches the broker's queue and not the
    // client, and a session that just started draws nothing.
    assert!(reader.drain().is_empty());
}

#[test]
fn a_delivery_reaches_the_client_and_wakes_the_loop_once() {
    let (reader, threads) = reader();
    let woken = counting(&threads);

    read(
        &threads,
        [Ok(publish(
            STATUS,
            r#"{"displayName":"The Theater","activity":"Idle"}"#,
            true,
        ))]
        .into_iter(),
    );

    assert_eq!(reader.drain().len(), 1);
    assert_eq!(woken.load(Ordering::SeqCst), 1);
}

#[test]
fn a_fold_that_draws_nothing_wakes_nothing() {
    let (reader, threads) = reader();
    let woken = counting(&threads);

    // A mark is a state the gate reads, and the catch-up draws nothing.
    read(&threads, [Ok(publish(FOCUS, "theater", true))].into_iter());

    assert!(reader.drain().is_empty());
    assert_eq!(woken.load(Ordering::SeqCst), 0);
}

#[test]
fn a_payload_that_decodes_to_nothing_reaches_no_client() {
    let (reader, threads) = reader();

    read(
        &threads,
        [Ok(publish(EVENTS, "not json", false))].into_iter(),
    );

    assert!(reader.drain().is_empty());
}

#[test]
fn a_failed_session_is_a_line_and_the_loop_runs_on() {
    let (reader, threads) = reader();

    read(
        &threads,
        [
            Err(ConnectionError::RequestsDone),
            Ok(publish(
                STATUS,
                r#"{"displayName":"The Theater","activity":"Idle"}"#,
                true,
            )),
        ]
        .into_iter(),
    );

    assert_eq!(reader.drain().len(), 1);
}

#[test]
fn an_event_this_crate_reads_nothing_from_changes_nothing() {
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

    // The rules are gone with the reader, so the first event ends the loop
    // and the second one never decodes.
    read(
        &threads,
        [
            Ok(publish(STATUS, r#"{"activity":"Idle"}"#, true)),
            Ok(publish(STATUS, r#"{"activity":"Playing"}"#, true)),
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
        [Ok(publish(STATUS, r#"{"activity":"Idle"}"#, true))].into_iter(),
    );

    // The send failed, so the status never reached the client and the loop
    // returned before it read the second event.
    assert!(reader.drain().is_empty());
}

#[test]
fn the_client_asks_for_the_shade_and_reads_it_back() {
    let (reader, _threads) = reader();
    idling(&reader);

    reader.sleep();

    assert_eq!(reader.drain(), [Moment::Sleep]);
}

#[test]
fn the_clock_sleeps_to_the_armed_window_and_never_longer_than_a_tick() {
    let now = Instant::now();
    assert_eq!(
        wait(Some(now + Duration::from_millis(200)), now),
        Duration::from_millis(200)
    );
    assert_eq!(wait(Some(now + Duration::from_secs(600)), now), TICK);
    assert_eq!(wait(None, now), TICK);
    assert_eq!(
        wait(Some(now), now + Duration::from_secs(1)),
        Duration::ZERO
    );
}

#[test]
fn the_clock_runs_the_windows_and_ends_with_the_client() {
    // A quiet window short enough to run out inside the test, so the clock's
    // first tick brings the shade down and the client reads it.
    let (reader, threads) = reader_over(Wiring {
        fade_after: Duration::from_millis(5),
        ..wiring()
    });
    idling(&reader);

    let running = threads.clone();
    let thread = std::thread::spawn(move || clock(&running));
    while reader.drain().is_empty() {
        std::thread::sleep(Duration::from_millis(10));
    }

    // The rules go with the reader, so the thread ends on its next pass.
    drop(reader);
    thread.join().expect("the clock ends");
}

#[test]
fn a_debug_line_names_the_reader_and_nothing_inside_it() {
    let (reader, _threads) = reader();

    assert_eq!(format!("{reader:?}"), "Reader { .. }");
}

#[test]
fn the_client_identifier_names_the_client_and_the_machine_it_runs_on() {
    assert_eq!(
        client_id("idle-screen", "den-tv-idle-7f4\n"),
        "idle-screen-den-tv-idle-7f4"
    );
    assert_eq!(client_id("idle-screen", ""), "idle-screen");
}

#[test]
fn the_machine_answers_to_a_name_or_to_none() {
    // A workstation and a pod both have the file; a container that does not
    // reads an empty name, and the identifier is then the client's own.
    let _ = hostname();
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
fn a_reader_takes_the_loops_waker_and_ends_with_the_client() {
    // Port 1 answers nothing, so the reader thread reports a failed session
    // and waits, the way it does while a broker is down. Both threads hold a
    // weak reference to the rules, so the drop below ends them.
    let reader = Reader::open(
        &Wiring {
            bus_address: "127.0.0.1:1".into(),
            ..wiring()
        },
        "media-screen-test",
    )
    .expect("the wiring names a broker and topics");

    reader.wake_on_delivery(Arc::new(|| {}));

    assert!(reader.drain().is_empty());
}

/// A broker for one client, speaking as much MQTT 3.1.1 as this test needs:
/// it answers the CONNECT and then reads. It parses nothing, because what
/// this test measures is that the client's own bytes reach the one
/// connection the crate holds.
///
/// The thread ends when it finds `wanted` in what the client wrote, or when
/// the deadline passes, and it reports which. The port is taken from the
/// kernel, so two runs never collide.
fn listening(wanted: Vec<u8>) -> (u16, std::thread::JoinHandle<bool>) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("the broker takes a port");
    let port = listener.local_addr().expect("the port is known").port();

    let thread = std::thread::spawn(move || {
        let (mut client, _) = listener.accept().expect("the client connects");
        client
            .set_read_timeout(Some(Duration::from_millis(100)))
            .expect("the reads are bounded");
        // The CONNECT, read and not parsed, and then the acknowledgement
        // that lets the client flush what it queued.
        let _ = client.read(&mut [0_u8; 1024]);
        client
            .write_all(&[0x20, 0x02, 0x00, 0x00])
            .expect("the connack goes out");

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

/// One QoS 0 PUBLISH as it travels. The topic and the payload are both
/// short, so the remaining length is the one byte the protocol uses under
/// 128.
fn packet(topic: &str, payload: &[u8], retained: bool) -> Vec<u8> {
    let length = 2 + topic.len() + payload.len();
    assert!(length < 128, "this test writes one length byte");

    let mut wire = vec![0x30 | u8::from(retained), length as u8];
    wire.extend((topic.len() as u16).to_be_bytes());
    wire.extend(topic.as_bytes());
    wire.extend(payload);
    wire
}

#[test]
fn a_clients_own_publish_goes_out_on_the_one_connection() {
    // The topic is the client's own, so this crate neither subscribes to
    // it nor reads what comes back on it.
    let topic = "liken/library/plays/house/requests";
    let payload = br#"{"list":"friday"}"#.to_vec();
    let (port, broker) = listening(packet(topic, &payload, true));

    let reader = Reader::open(
        &Wiring {
            bus_address: format!("127.0.0.1:{port}"),
            ..wiring()
        },
        "media-screen-test",
    )
    .expect("the wiring names a broker and topics");

    reader.publish(topic, payload, true);

    assert!(
        broker.join().expect("the broker thread ends"),
        "the publish reached the connection the reader already holds"
    );
    // The topic is the client's, so nothing about it reaches the rules.
    assert!(reader.drain().is_empty());
}
