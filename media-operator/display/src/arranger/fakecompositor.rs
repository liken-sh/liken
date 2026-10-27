//! A compositor that speaks the Wayland wire protocol on one end of a socket
//! pair, for the link's tests. It answers the core requests the binding
//! makes, offers `liken_arranger_manager_v1` when a test asks it to, and
//! answers the arranger the way the compositor module does: it grants or
//! refuses, reports the region and the surfaces, and reports `landed` for
//! each commit. It records every arranger request it reads, so a test reads
//! what the display sent.
//!
//! A message is the object id, then the size and the opcode in one word,
//! then the arguments in native byte order, each padded to four bytes.

use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::sync::{Arc, Mutex};

/// What the compositor offers and how it answers `get_arranger`.
#[derive(Debug, Clone, Copy)]
pub enum Offer {
    /// No arranger global: a compositor without the module.
    Nothing,
    /// Grant the arranger, and report a region of this size and one surface
    /// of mpv's.
    Grant(i32, i32),
    /// Refuse with this reason.
    Refuse(u32),
    /// Grant, then close the socket, the way a compositor that crashes does.
    GrantThenClose,
}

/// The arranger requests the compositor read, in order, as text.
pub type Log = Arc<Mutex<Vec<String>>>;

/// Serve one client on `stream` until it closes the socket.
pub fn serve(stream: UnixStream, offer: Offer) -> Log {
    let log = Log::default();
    let record = Arc::clone(&log);
    std::thread::spawn(move || Compositor::new(stream, offer, record).run());
    log
}

struct Compositor {
    stream: UnixStream,
    offer: Offer,
    log: Log,
    /// The interface of each object id the client created.
    objects: HashMap<u32, &'static str>,
}

const DISPLAY: u32 = 1;
const MANAGER_NAME: u32 = 7;

impl Compositor {
    fn new(stream: UnixStream, offer: Offer, log: Log) -> Self {
        Self {
            stream,
            offer,
            log,
            objects: HashMap::from([(DISPLAY, "wl_display")]),
        }
    }

    fn run(mut self) {
        while let Some((object, opcode, body)) = self.read() {
            let mut args = Args(&body);
            match (self.objects.get(&object).copied(), opcode) {
                // wl_display.sync: answer done at once, and free the id.
                (Some("wl_display"), 0) => {
                    let callback = args.uint();
                    self.send(callback, 0, &[Arg::Uint(0)]);
                    self.send(DISPLAY, 1, &[Arg::Uint(callback)]);
                }
                // wl_display.get_registry: list the globals.
                (Some("wl_display"), 1) => {
                    let registry = args.uint();
                    self.objects.insert(registry, "wl_registry");
                    if !matches!(self.offer, Offer::Nothing) {
                        self.send(
                            registry,
                            0,
                            &[
                                Arg::Uint(MANAGER_NAME),
                                Arg::Str("liken_arranger_manager_v1"),
                                Arg::Uint(1),
                            ],
                        );
                    }
                }
                // wl_registry.bind with a new_id of no fixed interface.
                (Some("wl_registry"), 0) => {
                    let _name = args.uint();
                    let _interface = args.string();
                    let _version = args.uint();
                    let id = args.uint();
                    self.objects.insert(id, "liken_arranger_manager_v1");
                }
                (Some("liken_arranger_manager_v1"), 0) => self.note("manager destroy"),
                (Some("liken_arranger_manager_v1"), 1) => {
                    let id = args.uint();
                    self.objects.insert(id, "liken_arranger_v1");
                    if !self.answer(id) {
                        return;
                    }
                }
                (Some("liken_arranger_v1"), 0) => self.note("arranger destroy"),
                (Some("liken_arranger_v1"), 1) => {
                    let app_id = args.string();
                    let (x, y, width, height) = (args.int(), args.int(), args.int(), args.int());
                    let opacity = f64::from(args.int()) / 256.0;
                    let (fit, delay) = (args.uint(), args.uint());
                    self.note(&format!(
                        "entry {app_id} {x},{y} {width}x{height} opacity {opacity} fit {fit} delay {delay}"
                    ));
                }
                (Some("liken_arranger_v1"), 2) => {
                    let serial = args.uint();
                    let name = args.string();
                    let (duration, curve) = (args.uint(), args.uint());
                    self.note(&format!(
                        "commit {serial} {name} {duration} ms curve {curve}"
                    ));
                    self.send(object, 4, &[Arg::Uint(serial)]);
                }
                _ => {}
            }
        }
    }

    /// Answer `get_arranger` the way the offer says. It answers false when
    /// the compositor goes away.
    fn answer(&mut self, arranger: u32) -> bool {
        match self.offer {
            Offer::Nothing => true,
            Offer::Refuse(reason) => {
                self.send(arranger, 0, &[Arg::Uint(reason)]);
                true
            }
            Offer::Grant(width, height) => {
                self.send(arranger, 1, &[]);
                self.send(arranger, 2, &[Arg::Int(width), Arg::Int(height)]);
                self.send(arranger, 3, &[Arg::Str(super::VIDEO), Arg::Uint(1)]);
                true
            }
            Offer::GrantThenClose => {
                self.send(arranger, 1, &[]);
                let _ = self.stream.shutdown(std::net::Shutdown::Both);
                false
            }
        }
    }

    fn note(&self, line: &str) {
        self.log.lock().expect("the log").push(line.to_string());
    }

    fn read(&mut self) -> Option<(u32, u16, Vec<u8>)> {
        let mut header = [0u8; 8];
        self.stream.read_exact(&mut header).ok()?;
        let object = u32::from_ne_bytes(header[0..4].try_into().ok()?);
        let word = u32::from_ne_bytes(header[4..8].try_into().ok()?);
        let mut body = vec![0u8; (word >> 16) as usize - 8];
        self.stream.read_exact(&mut body).ok()?;
        Some((object, (word & 0xffff) as u16, body))
    }

    fn send(&mut self, object: u32, opcode: u16, args: &[Arg<'_>]) {
        let mut body = Vec::new();
        for arg in args {
            match arg {
                Arg::Uint(value) => body.extend(value.to_ne_bytes()),
                Arg::Int(value) => body.extend(value.to_ne_bytes()),
                Arg::Str(text) => {
                    body.extend((text.len() as u32 + 1).to_ne_bytes());
                    body.extend(text.as_bytes());
                    body.push(0);
                    while body.len() % 4 != 0 {
                        body.push(0);
                    }
                }
            }
        }
        let mut message = object.to_ne_bytes().to_vec();
        message.extend((((body.len() as u32 + 8) << 16) | u32::from(opcode)).to_ne_bytes());
        message.extend(body);
        let _ = self.stream.write_all(&message);
    }
}

enum Arg<'a> {
    Uint(u32),
    Int(i32),
    Str(&'a str),
}

/// The arguments of one request, read in order.
struct Args<'a>(&'a [u8]);

impl Args<'_> {
    fn uint(&mut self) -> u32 {
        let (word, rest) = self.0.split_at(4);
        self.0 = rest;
        u32::from_ne_bytes(word.try_into().expect("four bytes"))
    }

    fn int(&mut self) -> i32 {
        self.uint() as i32
    }

    fn string(&mut self) -> String {
        let length = self.uint() as usize;
        let padded = length.div_ceil(4) * 4;
        let (text, rest) = self.0.split_at(padded);
        self.0 = rest;
        String::from_utf8_lossy(&text[..length.saturating_sub(1)]).into_owned()
    }
}
