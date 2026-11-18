//! The display connects to mpv's JSON IPC socket, the same socket that
//! the command sidecar uses, and observes mpv's properties there. The
//! sidecar sends the display its messages as `script-message` commands,
//! and mpv sends each one to every IPC client as a `client-message` event.

use std::collections::HashSet;
use std::io;
use std::path::{Path, PathBuf};
use std::time::Duration;

use serde_json::{Value, json};
use tokio::io::{AsyncBufReadExt, AsyncRead, AsyncWrite, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::sync::broadcast;
use tokio::sync::mpsc::Sender;

use crate::record::{self, Notes};

/// mpv serves its socket on an `emptyDir` volume of the playback pod, not
/// in the player container's `/tmp`, because the command sidecar is a
/// second container that connects to the same socket.
pub const SOCKET_PATH: &str = "/ipc/mpv.sock";

/// Overrides `SOCKET_PATH`, for a local mpv that serves its socket
/// somewhere else. The pod does not set it.
pub const SOCKET_VARIABLE: &str = "MEDIA_MPV_SOCKET";

/// mpv sends each observed property when the observer registers and again
/// on every change, so the display reads none of them on a timer. The
/// display keeps the latest value of each, so drawing a frame sends no
/// request to mpv.
pub const OBSERVED: [&str; 14] = [
    "duration",
    "time-pos",
    "chapter",
    "chapter-list",
    "chapter-metadata/by-key/title",
    "metadata",
    "track-list",
    "media-title",
    "path",
    "playlist-pos",
    "playlist-count",
    "pause",
    "audio-delay",
    "sub-delay",
];

/// The message that shows the display and arms the idle hide timer. It
/// does not move focus.
pub const SUMMON: &str = "summon";

/// The display sends this script message once, after it registers its
/// observers. mpv does not keep a message for a client that connects
/// later, so the sidecar can send the current item's block before the
/// display is there to receive it. The sidecar sends the block again when
/// it receives this message.
pub const PRESENTATION_REQUEST: &str = "liken-presentation-request";

/// The message the sidecar sends with the current item's block, as one JSON
/// string.
pub const PRESENTATION: &str = "presentation";

/// The message the sidecar sends with the Play's up-next block, as one JSON
/// string. It sends it at start and again each time it resends the
/// presentation. When the Play has no next work the sidecar sends none, and
/// the display draws no up-next card.
pub const NEXT: &str = "next";

/// The message the sidecar sends when a new message arrives on the
/// `Player`'s volume topic. Its arguments are the level from 0.0 to 1.0,
/// `yes` or `no` for mute, and `yes` or `no` for whether the display draws
/// the volume row, as in `volume-changed 0.63 no yes`. The sidecar does not
/// send it for the retained message it receives when it subscribes,
/// because that message restores the volume of a starting pod and is not a
/// change that a person made.
pub const VOLUME_CHANGED: &str = "volume-changed";

/// The request id of a command whose reply the display does not match to
/// its sender. Observes and the commands of key presses use other ids.
const UNMATCHED_ID: u64 = 0;

/// How long a connection attempt, or the gate's read of its properties,
/// waits before it fails.
const DIAL_TIMEOUT: Duration = Duration::from_secs(5);

/// How long to wait between connection attempts.
const DIAL_DELAY: Duration = Duration::from_secs(1);

/// The properties that `Ipc::playing` reads, each with the request id of
/// its reply. `time-pos` is a number once the film plays, and
/// `vo-configured` is true once mpv has created its video surface. The
/// gate reads both, because mpv reports a `time-pos` before it maps its
/// surface.
const GATE: [(u64, &str); 2] = [(1, "time-pos"), (2, "vo-configured")];

/// What the display reads off the socket.
#[derive(Debug, Clone, PartialEq)]
pub enum Event {
    /// An observed property has a new value.
    Property { name: String, value: Value },
    /// A `script-message` arrived, with its arguments.
    Message(Vec<String>),
    /// The socket closed. `Ipc::serve` connects again.
    Detached,
}

/// One line read from mpv's socket.
#[derive(Debug, Clone, PartialEq)]
pub enum Incoming {
    /// The reply to a command. `error` is `success` when mpv ran the
    /// command.
    Reply {
        id: u64,
        data: Value,
        error: String,
    },
    Event(Event),
}

/// A command for mpv: the command name followed by its arguments.
pub type Command = Vec<Value>;

/// The line for a command whose reply is not matched to its sender. The
/// frame loop has no socket, so it sends the line to the socket task over
/// a channel.
pub fn line(command: &Command) -> String {
    self::command(UNMATCHED_ID, command.clone())
}

/// One command as a line of newline-delimited JSON. mpv copies `id` into
/// the reply as `request_id`.
pub fn command(id: u64, arguments: Vec<Value>) -> String {
    format!("{}\n", json!({ "command": arguments, "request_id": id }))
}

/// The command that makes mpv send a property now and on every change.
pub fn observe(id: u64, name: &str) -> String {
    command(id, vec![json!("observe_property"), json!(id), json!(name)])
}

pub fn get_property(id: u64, name: &str) -> String {
    command(id, vec![json!("get_property"), json!(name)])
}

/// Parse one line from mpv. It returns `None` for a line that is not JSON
/// and for an event the display does not use.
pub fn decode(line: &str) -> Option<Incoming> {
    // Fields are moved out of the parsed value instead of cloned, because a
    // `track-list` change carries the whole list.
    let Value::Object(mut message) = serde_json::from_str::<Value>(line).ok()? else {
        return None;
    };
    let event = message.remove("event");
    match event.as_ref().and_then(Value::as_str) {
        Some("property-change") => {
            let Some(Value::String(name)) = message.remove("name") else {
                return None;
            };
            let value = message.remove("data").unwrap_or(Value::Null);
            Some(Incoming::Event(Event::Property { name, value }))
        }
        Some("client-message") => {
            let Some(Value::Array(arguments)) = message.remove("args") else {
                return None;
            };
            let arguments = arguments
                .into_iter()
                .map(|argument| match argument {
                    Value::String(word) => word,
                    other => other.to_string(),
                })
                .collect();
            Some(Incoming::Event(Event::Message(arguments)))
        }
        Some(_) => None,
        None => {
            let id = message.get("request_id")?.as_u64()?;
            let data = message.remove("data").unwrap_or(Value::Null);
            let error = match message.remove("error") {
                Some(Value::String(error)) => error,
                _ => String::new(),
            };
            Some(Incoming::Reply { id, data, error })
        }
    }
}

/// The `error` of a reply to a command that mpv ran. Any other value is
/// mpv's reason for refusing the command.
const SUCCESS: &str = "success";

/// The mpv socket the display connects to.
#[derive(Debug, Clone)]
pub struct Ipc {
    path: PathBuf,
}

impl Default for Ipc {
    fn default() -> Self {
        Self::at(SOCKET_PATH)
    }
}

impl Ipc {
    /// The socket at `SOCKET_VARIABLE` if it is set, otherwise `SOCKET_PATH`.
    pub fn from_environment() -> Self {
        match std::env::var(SOCKET_VARIABLE) {
            Ok(path) if !path.is_empty() => Self::at(path),
            _ => Self::default(),
        }
    }

    pub fn at(path: impl AsRef<Path>) -> Self {
        Self {
            path: path.as_ref().to_path_buf(),
        }
    }

    /// The socket path. The frame loop uses it as the identity of its
    /// subscription, so it opens one reader per path.
    pub fn path(&self) -> &Path {
        &self.path
    }

    /// Waits until mpv reports a playback position and has created its video
    /// surface. The compositor stacks a claim's surfaces with the newest on
    /// top, so the display must open its window after mpv's surface exists
    /// to be drawn above the film.
    ///
    /// Returns false at the deadline, so that a player that never starts
    /// makes the container exit instead of running with no window.
    pub async fn playing(&self, within: Duration) -> bool {
        let deadline = tokio::time::Instant::now() + within;
        while tokio::time::Instant::now() < deadline {
            if let Ok(Ok(stream)) =
                tokio::time::timeout(DIAL_TIMEOUT, UnixStream::connect(&self.path)).await
                && let Ok(Ok(true)) = tokio::time::timeout(DIAL_TIMEOUT, showing(stream)).await
            {
                return true;
            }
            tokio::time::sleep(DIAL_DELAY).await;
        }
        false
    }

    /// Connect to the socket, observe every property, and forward commands
    /// until the receiver of `events` is dropped. When the socket closes it
    /// sends `Detached` and connects again, because the display keeps
    /// running when mpv restarts.
    pub async fn serve(
        &self,
        events: Sender<Event>,
        commands: &broadcast::Sender<String>,
        notes: &Notes,
    ) {
        loop {
            match tokio::time::timeout(DIAL_TIMEOUT, UnixStream::connect(&self.path)).await {
                Ok(Ok(stream)) => {
                    let _ = session(stream, &events, &mut commands.subscribe(), notes).await;
                    // mpv cannot reply on a closed socket, so print the
                    // log line of each command still waiting for a reply.
                    for line in record::abandoned(notes) {
                        eprintln!("{line}");
                    }
                    if events.send(Event::Detached).await.is_err() {
                        return;
                    }
                }
                _ => {
                    if events.is_closed() {
                        return;
                    }
                }
            }
            tokio::time::sleep(DIAL_DELAY).await;
        }
    }
}

/// Observe every property on a connected socket, then read lines until it
/// closes.
///
/// Reads have no timeout. A paused film writes nothing, so a quiet socket
/// is normal, and only the socket closing ends the session.
pub async fn session<S>(
    stream: S,
    events: &Sender<Event>,
    commands: &mut broadcast::Receiver<String>,
    notes: &Notes,
) -> io::Result<()>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let (reader, mut writer) = tokio::io::split(stream);
    for (index, name) in OBSERVED.iter().enumerate() {
        writer
            .write_all(observe(index as u64 + 1, name).as_bytes())
            .await?;
    }
    writer
        .write_all(line(&vec![json!("script-message"), json!(PRESENTATION_REQUEST)]).as_bytes())
        .await?;

    // The command of a key press has its own request id, and its reply
    // completes the log line that the frame loop saved in `notes`. Any other
    // reply cannot be matched to a press, so print each distinct error once,
    // and a command that the frame loop repeats every frame does not flood
    // the log.
    let mut refused: HashSet<String> = HashSet::new();
    let mut lines = BufReader::new(reader).lines();
    loop {
        tokio::select! {
            read = lines.next_line() => {
                let Some(read) = read? else { return Ok(()) };
                match decode(&read) {
                    Some(Incoming::Event(event)) => {
                        if events.send(event).await.is_err() {
                            return Ok(());
                        }
                    }
                    Some(Incoming::Reply { id, error, .. }) if id >= record::FIRST_ID => {
                        if let Some(line) = record::answered(notes, id, &error) {
                            eprintln!("{line}");
                        }
                    }
                    Some(Incoming::Reply { error, .. })
                        if error != SUCCESS && refused.insert(error.clone()) =>
                    {
                        eprintln!("media-display: mpv refused a command: {error}");
                    }
                    _ => {}
                }
            }
            written = commands.recv() => {
                match written {
                    Ok(written) => writer.write_all(written.as_bytes()).await?,
                    Err(broadcast::error::RecvError::Closed) => return Ok(()),
                    Err(broadcast::error::RecvError::Lagged(_)) => {}
                }
            }
        }
    }
}

/// Read both gate properties once. Returns true when `time-pos` is a
/// number and `vo-configured` is true.
async fn showing<S>(stream: S) -> io::Result<bool>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let (reader, mut writer) = tokio::io::split(stream);
    for (id, name) in GATE {
        writer.write_all(get_property(id, name).as_bytes()).await?;
    }

    let (mut playing, mut configured) = (None, None);
    let mut lines = BufReader::new(reader).lines();
    while let Some(line) = lines.next_line().await? {
        match decode(&line) {
            Some(Incoming::Reply { id, data, .. }) if id == GATE[0].0 => {
                playing = Some(data.is_number());
            }
            Some(Incoming::Reply { id, data, .. }) if id == GATE[1].0 => {
                configured = Some(data == Value::Bool(true));
            }
            _ => continue,
        }
        if let (Some(playing), Some(configured)) = (playing, configured) {
            return Ok(playing && configured);
        }
    }
    Ok(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::AsyncReadExt;
    use tokio::sync::mpsc;

    #[test]
    fn a_command_is_one_line_of_json_carrying_its_request_id() {
        assert_eq!(
            command(7, vec![json!("get_property"), json!("time-pos")]),
            "{\"command\":[\"get_property\",\"time-pos\"],\"request_id\":7}\n"
        );
        assert_eq!(
            observe(3, "pause"),
            "{\"command\":[\"observe_property\",3,\"pause\"],\"request_id\":3}\n"
        );
        assert_eq!(
            get_property(1, "duration"),
            "{\"command\":[\"get_property\",\"duration\"],\"request_id\":1}\n"
        );
    }

    #[test]
    fn a_property_change_reads_as_the_property_and_its_value() {
        assert_eq!(
            decode(r#"{"event":"property-change","id":2,"name":"time-pos","data":12.5}"#),
            Some(Incoming::Event(Event::Property {
                name: "time-pos".to_string(),
                value: json!(12.5),
            }))
        );
        assert_eq!(
            decode(r#"{"event":"property-change","id":9,"name":"aid"}"#),
            Some(Incoming::Event(Event::Property {
                name: "aid".to_string(),
                value: Value::Null,
            }))
        );
    }

    #[test]
    fn a_client_message_reads_as_its_words() {
        assert_eq!(
            decode(r#"{"event":"client-message","args":["summon"]}"#),
            Some(Incoming::Event(Event::Message(vec!["summon".to_string()])))
        );
        assert_eq!(
            decode(
                r#"{"event":"client-message","args":["liken-art","logo","/art/a.bgra","760","110","3040"]}"#
            ),
            Some(Incoming::Event(Event::Message(vec![
                "liken-art".to_string(),
                "logo".to_string(),
                "/art/a.bgra".to_string(),
                "760".to_string(),
                "110".to_string(),
                "3040".to_string(),
            ])))
        );
    }

    #[test]
    fn a_reply_reads_as_its_request_id_and_its_datum() {
        assert_eq!(
            decode(r#"{"data":42.0,"request_id":1,"error":"success"}"#),
            Some(Incoming::Reply {
                id: 1,
                data: json!(42.0),
                error: "success".to_string(),
            })
        );
        assert_eq!(
            decode(r#"{"data":null,"request_id":1,"error":"property unavailable"}"#),
            Some(Incoming::Reply {
                id: 1,
                data: Value::Null,
                error: "property unavailable".to_string(),
            })
        );
    }

    #[test]
    fn a_line_the_display_does_not_use_decodes_as_none() {
        assert_eq!(decode("not json at all"), None);
        assert_eq!(decode(r#"{"event":"seek"}"#), None);
        assert_eq!(decode(r#"{"event":"property-change","id":1}"#), None);
        assert_eq!(decode(r#"{"event":"client-message"}"#), None);
        assert_eq!(decode("{}"), None);
    }

    #[tokio::test]
    async fn a_session_observes_every_property_and_asks_for_the_block() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, _events) = mpsc::channel(64);
        let (commands, mut receiver) = broadcast::channel(64);
        let session = tokio::spawn(async move {
            let _ = session(display, &sender, &mut receiver, &Notes::default()).await;
        });

        let sent = read_lines(&mut mpv, OBSERVED.len() + 1).await;
        assert_eq!(sent.len(), OBSERVED.len() + 1);
        for (index, name) in OBSERVED.iter().enumerate() {
            assert_eq!(sent[index], observe(index as u64 + 1, name).trim_end());
        }
        assert_eq!(
            sent[OBSERVED.len()],
            "{\"command\":[\"script-message\",\"liken-presentation-request\"],\"request_id\":0}"
        );

        drop(commands);
        drop(mpv);
        session
            .await
            .expect("ending the session by closing the socket");
    }

    #[tokio::test]
    async fn a_command_the_frame_loop_sends_reaches_mpv() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, _events) = mpsc::channel(64);
        let (commands, mut receiver) = broadcast::channel(64);
        let session = tokio::spawn(async move {
            let _ = session(display, &sender, &mut receiver, &Notes::default()).await;
        });

        let _ = read_lines(&mut mpv, OBSERVED.len() + 1).await;
        commands
            .send(line(&vec![
                json!("seek"),
                json!(12.5),
                json!("absolute+exact"),
            ]))
            .expect("sending the command to the session");
        assert_eq!(
            read_lines(&mut mpv, 1).await,
            vec!["{\"command\":[\"seek\",12.5,\"absolute+exact\"],\"request_id\":0}"]
        );

        drop(commands);
        session
            .await
            .expect("ending the session by closing the channel");
    }

    /// A reply with a press's request id removes that press's line from
    /// the notes. A reply to a command with id 0 leaves them alone.
    #[tokio::test]
    async fn a_reply_completes_the_log_line_of_its_press() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, _events) = mpsc::channel(64);
        let (_commands, mut receiver) = broadcast::channel(64);
        let notes = Notes::default();
        for id in [record::FIRST_ID, record::FIRST_ID + 1] {
            notes
                .lock()
                .expect("locking the notes")
                .insert(id, format!("line {id}"));
        }
        let held = notes.clone();
        let session = tokio::spawn(async move {
            let _ = session(display, &sender, &mut receiver, &held).await;
        });

        let _ = read_lines(&mut mpv, OBSERVED.len() + 1).await;
        mpv.write_all(
            b"{\"request_id\":1000,\"error\":\"success\"}\n{\"request_id\":0,\"error\":\"success\"}\n",
        )
        .await
        .expect("writing the replies to the session");
        drop(mpv);
        session
            .await
            .expect("ending the session by closing the socket");

        assert_eq!(
            notes
                .lock()
                .expect("locking the notes")
                .keys()
                .copied()
                .collect::<Vec<_>>(),
            vec![record::FIRST_ID + 1]
        );
    }

    /// Read lines from mpv's end of the pipe until there are `count`.
    async fn read_lines<S>(mpv: &mut S, count: usize) -> Vec<String>
    where
        S: AsyncRead + Unpin,
    {
        let mut buffer = vec![0u8; 8192];
        let mut sent = String::new();
        while sent.lines().count() < count {
            let read = mpv
                .read(&mut buffer)
                .await
                .expect("reading the line the session wrote");
            if read == 0 {
                break;
            }
            sent.push_str(&String::from_utf8_lossy(&buffer[..read]));
        }
        sent.lines().map(str::to_string).collect()
    }

    /// The session forwards each event that the display uses, skips the
    /// rest, and ends when the socket closes.
    #[tokio::test]
    async fn a_session_forwards_every_event_mpv_writes() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, mut events) = mpsc::channel(64);
        let (_commands, mut receiver) = broadcast::channel(64);
        let session = tokio::spawn(async move {
            let _ = session(display, &sender, &mut receiver, &Notes::default()).await;
        });

        mpv.write_all(
            concat!(
                "{\"event\":\"property-change\",\"id\":2,\"name\":\"time-pos\",\"data\":1.5}\n",
                "{\"event\":\"seek\"}\n",
                "{\"event\":\"client-message\",\"args\":[\"summon\"]}\n",
            )
            .as_bytes(),
        )
        .await
        .expect("writing the events to the session");

        assert_eq!(
            events.recv().await,
            Some(Event::Property {
                name: "time-pos".to_string(),
                value: json!(1.5),
            })
        );
        assert_eq!(
            events.recv().await,
            Some(Event::Message(vec!["summon".to_string()]))
        );

        drop(mpv);
        session
            .await
            .expect("ending the session by closing the socket");
        assert_eq!(events.recv().await, None);
    }

    #[tokio::test]
    async fn the_gate_needs_a_position_and_the_video_output() {
        for (position, output, want) in [
            ("12.5", "true", true),
            ("null", "true", false),
            ("12.5", "false", false),
            ("null", "false", false),
        ] {
            let (display, mut mpv) = tokio::io::duplex(8192);
            let gate = tokio::spawn(showing(display));

            let mut read = vec![0u8; 512];
            let count = mpv
                .read(&mut read)
                .await
                .expect("reading the requests the gate wrote");
            assert_eq!(
                String::from_utf8_lossy(&read[..count]),
                format!(
                    "{}{}",
                    get_property(GATE[0].0, GATE[0].1),
                    get_property(GATE[1].0, GATE[1].1)
                )
            );
            mpv.write_all(
                format!(
                    "{{\"data\":{position},\"request_id\":1,\"error\":\"success\"}}\n\
                     {{\"event\":\"seek\"}}\n\
                     {{\"data\":{output},\"request_id\":2,\"error\":\"success\"}}\n"
                )
                .as_bytes(),
            )
            .await
            .expect("writing the replies to the session");

            assert_eq!(gate.await.expect("joining the gate task").ok(), Some(want));
        }
    }

    /// With no socket to connect to, the gate returns false at its
    /// deadline.
    #[tokio::test]
    async fn the_gate_gives_up_at_its_deadline() {
        let ipc = Ipc::at("/nonexistent/mpv.sock");
        assert!(
            !ipc.playing(Duration::from_millis(1)).await,
            "the gate reports playing with no socket, want false"
        );
    }

    /// `serve` forwards mpv's events, sends `Detached` when the socket
    /// closes, and connects again.
    #[tokio::test]
    async fn serve_forwards_events_reports_the_close_and_connects_again() {
        let dir = std::env::temp_dir().join(format!("media-display-serve-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("creating the directory for the socket");
        let path = dir.join("mpv.sock");
        let listener = tokio::net::UnixListener::bind(&path).expect("binding the socket");

        let ipc = Ipc::at(&path);
        let (sender, mut events) = mpsc::channel(64);
        let (commands, _receiver) = broadcast::channel(64);
        let serving =
            tokio::spawn(async move { ipc.serve(sender, &commands, &Notes::default()).await });

        let (mut mpv, _) = listener
            .accept()
            .await
            .expect("accepting the first connection");
        let mut read = vec![0u8; 4096];
        let mut sent = String::new();
        while sent.lines().count() < OBSERVED.len() {
            let count = mpv
                .read(&mut read)
                .await
                .expect("reading the observe requests");
            sent.push_str(&String::from_utf8_lossy(&read[..count]));
        }
        mpv.write_all(b"{\"event\":\"client-message\",\"args\":[\"summon\"]}\n")
            .await
            .expect("writing one message to the display");
        assert_eq!(
            events.recv().await,
            Some(Event::Message(vec!["summon".to_string()]))
        );

        drop(mpv);
        assert_eq!(events.recv().await, Some(Event::Detached));

        tokio::time::timeout(Duration::from_secs(5), listener.accept())
            .await
            .expect("waiting for the second connection")
            .expect("accepting the second connection");

        serving.abort();
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// `serve` returns when the receiver of `events` is dropped, so a
    /// closed window leaves no reader task behind.
    #[tokio::test]
    async fn the_loop_ends_when_the_display_stops_reading() {
        let ipc = Ipc::at("/nonexistent/mpv.sock");
        let (sender, events) = mpsc::channel(1);
        drop(events);

        let (commands, _receiver) = broadcast::channel(64);
        tokio::time::timeout(
            Duration::from_secs(5),
            ipc.serve(sender, &commands, &Notes::default()),
        )
        .await
        .expect("ending the dial loop");
    }

    #[test]
    fn the_default_socket_is_the_pod_path() {
        assert_eq!(SOCKET_PATH, "/ipc/mpv.sock");
        assert_eq!(Ipc::default().path, PathBuf::from(SOCKET_PATH));
    }

    /// No other test in the crate reads or writes `SOCKET_VARIABLE`, so
    /// changing it here races no other test. The test restores its value.
    #[test]
    fn the_socket_variable_overrides_the_pod_path() {
        let found = std::env::var(SOCKET_VARIABLE).ok();

        // SAFETY: no other test reads or writes this variable.
        unsafe { std::env::set_var(SOCKET_VARIABLE, "/tmp/local.sock") };
        assert_eq!(
            Ipc::from_environment().path,
            PathBuf::from("/tmp/local.sock")
        );

        // SAFETY: the same.
        unsafe { std::env::set_var(SOCKET_VARIABLE, "") };
        assert_eq!(Ipc::from_environment().path, PathBuf::from(SOCKET_PATH));

        // SAFETY: the same.
        unsafe { std::env::remove_var(SOCKET_VARIABLE) };
        assert_eq!(Ipc::from_environment().path, PathBuf::from(SOCKET_PATH));

        if let Some(found) = found {
            // SAFETY: the same.
            unsafe { std::env::set_var(SOCKET_VARIABLE, found) };
        }
    }
}
