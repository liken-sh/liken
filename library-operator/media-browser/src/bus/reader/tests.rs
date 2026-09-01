// The reader against a broker of the test's own: one connection on a
// loopback socket that answers the connect and the subscribe, publishes one
// press, and reads back what the browser publishes. The thread and the
// publish are the two paths no fake reaches.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use super::*;
use crate::bus::screen;

const COMMANDS: &str = "liken/media/players/house/den-tv/commands";
const SCREEN: &str = "liken/media/players/house/den-tv/screen";
const PLAY: &str = "liken/library/players/house/den-tv/play";

// The longest a test waits for a press or a publish.
const CAP: Duration = Duration::from_secs(10);

// What the fixture broker read off the client's connection.
type Published = mpsc::Receiver<(String, Vec<u8>)>;

// A broker on a loopback port that answers one client: CONNACK for the
// connect, SUBACK for the subscribe, and one press on the commands topic once
// the client is subscribed. Every publish the client sends reaches the
// channel. rumqttc queues a publish ahead of the subscribe, so the answers
// are keyed on the packet type and not on an order.
fn fixture(press: &'static [u8]) -> (String, Published) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("a loopback port");
    let address = listener.local_addr().expect("the bound port").to_string();
    let (sender, published) = mpsc::channel();

    std::thread::spawn(move || {
        let (mut stream, _) = listener.accept().expect("the client connects");
        loop {
            let read = packet(&mut stream);
            match read.first().map(|first| first >> 4) {
                Some(CONNECT) => answer(&mut stream, &[0x20, 0x02, 0x00, 0x00]),
                Some(SUBSCRIBE) => {
                    answer(&mut stream, &suback(&read));
                    answer(&mut stream, &publication(COMMANDS, press));
                }
                Some(PUBLISH) => {
                    let _ = sender.send(topic_and_payload(&read));
                    // A second press, so a test that arms the waker
                    // before it publishes reads a delivery that cannot have
                    // landed first.
                    answer(&mut stream, &publication(COMMANDS, press));
                }
                _ => return,
            }
        }
    });

    (address, published)
}

// The MQTT packet types this broker answers.
const CONNECT: u8 = 1;
const PUBLISH: u8 = 3;
const SUBSCRIBE: u8 = 8;

// One answer on the connection; a client that went away ends the
// broker's thread rather than failing a test that already finished.
fn answer(stream: &mut TcpStream, packet: &[u8]) {
    let _ = stream.write_all(packet);
}

// One whole MQTT packet; an empty answer is a closed connection.
fn packet(stream: &mut TcpStream) -> Vec<u8> {
    let mut byte = [0u8; 1];
    if stream.read_exact(&mut byte).is_err() {
        return Vec::new();
    }
    let mut whole = vec![byte[0]];

    let (mut length, mut shift) = (0usize, 0);
    loop {
        if stream.read_exact(&mut byte).is_err() {
            return Vec::new();
        }
        length |= usize::from(byte[0] & 0x7f) << shift;
        shift += 7;
        if byte[0] & 0x80 == 0 {
            break;
        }
    }

    let mut body = vec![0u8; length];
    if stream.read_exact(&mut body).is_err() {
        return Vec::new();
    }
    whole.extend(body);
    whole
}

// The answer to one subscribe: its packet identifier and one granted
// qos per filter.
fn suback(subscribe: &[u8]) -> Vec<u8> {
    let body = &subscribe[1..];
    let mut at = 2;
    let mut filters = 0;
    while at < body.len() {
        at += 2 + ((usize::from(body[at]) << 8) | usize::from(body[at + 1])) + 1;
        filters += 1;
    }

    let mut answer = vec![0x90, (2 + filters) as u8, body[0], body[1]];
    answer.extend(std::iter::repeat_n(0u8, filters));
    answer
}

// One qos-0 publish, short enough that its length is one byte.
fn publication(topic: &str, payload: &[u8]) -> Vec<u8> {
    let mut body = vec![(topic.len() >> 8) as u8, topic.len() as u8];
    body.extend(topic.as_bytes());
    body.extend(payload);

    let mut whole = vec![0x30, body.len() as u8];
    whole.extend(body);
    whole
}

// The topic and the payload of one qos-0 publish.
fn topic_and_payload(publish: &[u8]) -> (String, Vec<u8>) {
    let body = &publish[1..];
    let length = (usize::from(body[0]) << 8) | usize::from(body[1]);
    (
        String::from_utf8_lossy(&body[2..2 + length]).into_owned(),
        body[2 + length..].to_vec(),
    )
}

// The first message the reader delivers, or a panic at the cap.
fn delivered(reader: &Reader) -> Message {
    let deadline = Instant::now() + CAP;
    while Instant::now() < deadline {
        if let Some(message) = reader.drain().into_iter().next() {
            return message;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    panic!("nothing reached the browser inside {CAP:?}");
}

fn reader(address: &str) -> Reader {
    named(address, PLAY.into())
}

// A reader with the play topic the test names, so a test drives both the
// operator that named one and the older operator that named none.
fn named(address: &str, play: String) -> Reader {
    Reader::open(
        address,
        "media-browser-test",
        COMMANDS.into(),
        SCREEN.into(),
        play,
    )
    .expect("the reader opens against an address and two topics")
}

#[test]
fn a_press_on_the_commands_topic_reaches_the_browser() {
    let (address, _published) = fixture(br#"{"key":"KEY_UP","value":1}"#);

    assert_eq!(delivered(&reader(&address)), Message::Press("up"));
}

#[test]
fn a_moment_this_browser_does_not_name_reaches_nothing() {
    let (address, published) = fixture(br#"{"key":"KEY_MUTE","value":1}"#);
    let reader = reader(&address);

    // The sleep request that follows proves the connection carried
    // the ignored press first.
    reader.request_sleep();
    assert_eq!(
        published.recv_timeout(CAP).expect("the sleep request"),
        (COMMANDS.to_string(), SLEEP.to_vec())
    );
    assert_eq!(reader.drain(), Vec::new());
}

#[test]
fn the_sleep_request_reaches_the_commands_topic() {
    let (address, published) = fixture(br#"{"key":"KEY_UP","value":1}"#);
    let reader = reader(&address);

    reader.request_sleep();

    assert_eq!(
        published.recv_timeout(CAP).expect("the sleep request"),
        (COMMANDS.to_string(), SLEEP.to_vec())
    );
}

#[test]
fn a_broker_that_answers_nothing_delivers_nothing() {
    // A bound and released port answers no connection, so the session
    // fails and the thread waits to try again.
    let listener = TcpListener::bind("127.0.0.1:0").expect("a loopback port");
    let address = listener.local_addr().expect("the bound port").to_string();
    drop(listener);

    let reader = reader(&address);
    std::thread::sleep(Duration::from_millis(200));

    assert_eq!(reader.drain(), Vec::new());
}

#[test]
fn a_delivery_wakes_the_loop() {
    let (address, _published) = fixture(br#"{"key":"KEY_UP","value":1}"#);
    let reader = reader(&address);
    let woken = std::sync::Arc::new(AtomicUsize::new(0));
    let counter = std::sync::Arc::clone(&woken);
    reader.wake_on_delivery(std::sync::Arc::new(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    }));

    reader.request_sleep();

    assert!(waits_for(&woken));
}

// Whether the counter moved inside the cap.
fn waits_for(counter: &AtomicUsize) -> bool {
    let deadline = Instant::now() + CAP;
    while Instant::now() < deadline {
        if counter.load(Ordering::SeqCst) > 0 {
            return true;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    false
}

#[test]
fn a_delivery_sends_the_message_and_wakes_the_loop() {
    let (sender, messages) = mpsc::channel();
    let woken = std::sync::Arc::new(AtomicUsize::new(0));
    let counter = std::sync::Arc::clone(&woken);
    let wake: Waker = std::sync::Arc::new(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    });
    let waker: WakerSlot = std::sync::Arc::new(std::sync::Mutex::new(Some(wake)));

    assert!(forward(
        &Session::new(COMMANDS.into(), SCREEN.into()),
        SCREEN,
        br#"{"event":"sleep"}"#,
        &sender,
        &waker
    ));

    assert_eq!(
        messages.try_iter().collect::<Vec<_>>(),
        vec![Message::Screen(screen::Event::Sleep)]
    );
    assert_eq!(woken.load(Ordering::SeqCst), 1);
}

#[test]
fn a_payload_that_decodes_to_nothing_sends_nothing_and_wakes_nothing() {
    let (sender, messages) = mpsc::channel();
    let woken = std::sync::Arc::new(AtomicUsize::new(0));
    let counter = std::sync::Arc::clone(&woken);
    let wake: Waker = std::sync::Arc::new(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    });
    let waker: WakerSlot = std::sync::Arc::new(std::sync::Mutex::new(Some(wake)));

    assert!(forward(
        &Session::new(COMMANDS.into(), SCREEN.into()),
        COMMANDS,
        b"loud",
        &sender,
        &waker
    ));

    assert_eq!(messages.try_iter().count(), 0);
    assert_eq!(woken.load(Ordering::SeqCst), 0);
}

#[test]
fn a_dropped_receiver_ends_the_thread() {
    let (sender, messages) = mpsc::channel();
    drop(messages);
    let waker: WakerSlot = std::sync::Arc::default();

    assert!(!forward(
        &Session::new(COMMANDS.into(), SCREEN.into()),
        COMMANDS,
        br#"{"key":"KEY_UP","value":1}"#,
        &sender,
        &waker
    ));
}

#[test]
fn a_browser_with_no_broker_and_one_with_no_topics_open_no_reader() {
    assert!(
        Reader::open(
            "",
            "media-browser",
            COMMANDS.into(),
            SCREEN.into(),
            PLAY.into()
        )
        .is_none()
    );
    assert!(
        Reader::open(
            "broker:1883",
            "media-browser",
            String::new(),
            SCREEN.into(),
            PLAY.into()
        )
        .is_none()
    );
    assert!(
        Reader::open(
            "broker:1883",
            "media-browser",
            COMMANDS.into(),
            String::new(),
            PLAY.into()
        )
        .is_none()
    );
}

#[test]
fn a_play_request_reaches_the_topic_the_operator_named() {
    let (address, published) = fixture(br#"{"key":"KEY_UP","value":1}"#);
    let reader = reader(&address);

    reader.request_play(br#"{"library":"default/films"}"#.to_vec());

    assert_eq!(
        published.recv_timeout(CAP).expect("the play request"),
        (PLAY.to_string(), br#"{"library":"default/films"}"#.to_vec())
    );
}

#[test]
fn a_browser_with_no_play_topic_publishes_no_request() {
    let (address, published) = fixture(br#"{"key":"KEY_UP","value":1}"#);
    let reader = named(&address, String::new());

    reader.request_play(br#"{"library":"default/films"}"#.to_vec());
    // The sleep request that follows proves the connection carried the
    // dropped request first.
    reader.request_sleep();

    assert_eq!(
        published.recv_timeout(CAP).expect("the sleep request"),
        (COMMANDS.to_string(), SLEEP.to_vec())
    );
}

#[test]
fn the_client_identifier_names_the_machine_it_runs_on() {
    assert_eq!(
        client_id("den-tv-media-browser\n"),
        "media-browser-den-tv-media-browser"
    );
    assert_eq!(client_id(""), "media-browser");
    assert!(client_id(&hostname()).starts_with("media-browser"));
}

#[test]
fn an_address_with_no_port_answers_on_the_mqtt_default() {
    assert_eq!(broker("broker"), Some(("broker".into(), 1883)));
    assert_eq!(broker(" broker:1884 "), Some(("broker".into(), 1884)));
}

#[test]
fn an_address_this_browser_cannot_read_is_no_broker() {
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
fn a_reader_prints_as_a_reader() {
    let (address, _published) = fixture(br#"{"key":"KEY_UP","value":1}"#);

    assert!(format!("{:?}", reader(&address)).starts_with("Reader"));
}
