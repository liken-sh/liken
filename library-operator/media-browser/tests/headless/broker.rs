// A broker for one client, so a headless run folds the moments a cluster
// would deliver.
//
// The browser reads its level off an MQTT topic through `media-screen`,
// and nothing else in the tree stands in for that. The client's own path
// is the thing under test, so the test opens a socket and answers the
// packets rather than reaching inside the crate: connect, subscribe, and
// the publishes that carry the level.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

// How often the broker states the level again. Every publish is a press, so
// a run of them holds the row on screen for the whole run.
const EVERY: Duration = Duration::from_millis(250);

/// A broker on loopback, and the address the client connects to.
pub struct Broker {
    pub address: String,
    stopped: Arc<AtomicBool>,
}

impl Drop for Broker {
    fn drop(&mut self) {
        self.stopped.store(true, Ordering::SeqCst);
    }
}

/// Answer one client, and publish this payload on this topic for as long as
/// the connection stands.
pub fn publishing(topic: &str, payload: &str) -> Broker {
    let listener = TcpListener::bind("127.0.0.1:0").expect("a port on loopback");
    let address = listener
        .local_addr()
        .expect("the port the kernel gave")
        .to_string();
    let stopped = Arc::new(AtomicBool::new(false));

    let (topic, payload) = (topic.to_string(), payload.to_string());
    let ending = Arc::clone(&stopped);
    std::thread::spawn(move || {
        let Ok((stream, _)) = listener.accept() else {
            return;
        };
        serve(stream, &topic, &payload, &ending);
    });

    Broker { address, stopped }
}

// One session: answer every packet the client sends, and publish on the
// topic once the client has subscribed.
fn serve(mut stream: TcpStream, topic: &str, payload: &str, ending: &Arc<AtomicBool>) {
    let mut writing = stream.try_clone().expect("a second handle on the socket");
    while let Some((kind, body)) = packet(&mut stream) {
        match kind {
            CONNECT => {
                let _ = writing.write_all(&[0x20, 0x02, 0x00, 0x00]);
            }
            SUBSCRIBE => {
                let _ = writing.write_all(&suback(&body));
                let message = publish(topic, payload);
                let mut publishing = writing.try_clone().expect("a handle for the publishes");
                let ending = Arc::clone(ending);
                std::thread::spawn(move || {
                    while publishing.write_all(&message).is_ok() {
                        if ending.load(Ordering::SeqCst) {
                            return;
                        }
                        std::thread::sleep(EVERY);
                    }
                });
            }
            PINGREQ => {
                let _ = writing.write_all(&[0xD0, 0x00]);
            }
            DISCONNECT => return,
            _ => {}
        }
        if ending.load(Ordering::SeqCst) {
            return;
        }
    }
}

// The MQTT packet types this broker answers.
const CONNECT: u8 = 1;
const SUBSCRIBE: u8 = 8;
const PINGREQ: u8 = 12;
const DISCONNECT: u8 = 14;

// One packet off the socket: its type, and its body. A packet's length is a
// run of bytes that each carry seven bits of it.
fn packet(stream: &mut TcpStream) -> Option<(u8, Vec<u8>)> {
    let mut header = [0u8; 1];
    stream.read_exact(&mut header).ok()?;

    let mut length = 0usize;
    let mut shift = 0;
    loop {
        let mut byte = [0u8; 1];
        stream.read_exact(&mut byte).ok()?;
        length += usize::from(byte[0] & 0x7F) << shift;
        if byte[0] & 0x80 == 0 {
            break;
        }
        shift += 7;
    }

    let mut body = vec![0u8; length];
    stream.read_exact(&mut body).ok()?;
    Some((header[0] >> 4, body))
}

// The answer to a subscribe: the packet identifier the client sent, and one
// granted quality of service for each filter it asked for.
fn suback(body: &[u8]) -> Vec<u8> {
    let mut granted = Vec::new();
    let mut at = 2;
    while at + 2 <= body.len() {
        let length = usize::from(u16::from_be_bytes([body[at], body[at + 1]]));
        at += 2 + length + 1;
        granted.push(0x00);
    }

    let mut packet = vec![0x90, (2 + granted.len()) as u8, body[0], body[1]];
    packet.extend(granted);
    packet
}

// One message on a topic, at the least quality of service and not retained.
// The client reads a message the broker did not retain as a press, which is
// what brings the row up.
fn publish(topic: &str, payload: &str) -> Vec<u8> {
    let mut body = (topic.len() as u16).to_be_bytes().to_vec();
    body.extend(topic.as_bytes());
    body.extend(payload.as_bytes());

    let mut packet = vec![0x30];
    let mut length = body.len();
    loop {
        let mut byte = (length % 128) as u8;
        length /= 128;
        if length > 0 {
            byte |= 0x80;
        }
        packet.push(byte);
        if length == 0 {
            break;
        }
    }
    packet.extend(body);
    packet
}
