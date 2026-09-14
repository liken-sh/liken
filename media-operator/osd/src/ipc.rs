//! The display is an IPC client of mpv, on the socket the command sidecar
//! already drives. It reads properties over that socket and observes the
//! ones the Lua display observes. Every message the sidecar sends the
//! display arrives as a `client-message` event, because mpv delivers a
//! `script-message` to every client and an IPC client cannot be named.

use std::io;
use std::path::{Path, PathBuf};
use std::time::Duration;

use serde_json::{Value, json};
use tokio::io::{AsyncBufReadExt, AsyncRead, AsyncWrite, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::sync::mpsc::Sender;

/// mpv serves its JSON IPC socket on an emptyDir the playback pod always
/// carries, rather than in the player container's private `/tmp`, because
/// the command sidecar drives the same socket and a volume is the only thing
/// two containers share.
pub const SOCKET_PATH: &str = "/ipc/mpv.sock";

/// The display reads a socket path on top of the default, for the reason the
/// bridge's own decode mode reads one: a local mpv serves its socket
/// somewhere other than the pod's fixed path. It takes a default, so the pod
/// sets nothing.
pub const SOCKET_VARIABLE: &str = "MEDIA_MPV_SOCKET";

/// The properties the display observes. mpv pushes each one once at
/// registration and then on every change.
///
pub const OBSERVED: [&str; 16] = [
    "duration",
    "time-pos",
    "percent-pos",
    "chapter",
    "chapter-list",
    "chapter-metadata/by-key/title",
    "metadata",
    "track-list",
    "aid",
    "media-title",
    "sid",
    "playlist-pos",
    "playlist-count",
    "pause",
    "volume",
    "mute",
];

/// The message that shows the display, arms the idle hide, and moves no
/// focus.
pub const SUMMON: &str = "summon";

/// The six navigation actions. The word is the whole of the message.
pub const ACTIONS: [&str; 6] = ["up", "down", "left", "right", "select", "back"];

/// How long a dial waits before it counts as a socket that does not answer.
const DIAL_TIMEOUT: Duration = Duration::from_secs(5);

/// How long the loop waits between two tries at the socket.
const DIAL_DELAY: Duration = Duration::from_secs(1);

/// The two properties the window gate reads, and the request id each one
/// comes back under. `time-pos` says the film is playing. `vo-configured`
/// says mpv's video output is up, which is when mpv's own surface exists.
///
/// The gate reads both because the first is not enough. mpv answers
/// `time-pos` with a number before it maps its surface, and a display that
/// opened its window on that answer alone arrived first, so the layout
/// stacked it under the film. The compositor stacks the newest surface on
/// top, and this gate is what makes the display's surface the newest.
const GATE: [(u64, &str); 2] = [(1, "time-pos"), (2, "vo-configured")];

/// What the display reads off the socket.
#[derive(Debug, Clone, PartialEq)]
pub enum Event {
    /// One observed property carries a new value.
    Property { name: String, value: Value },
    /// One `script-message` reached every client.
    Message(Vec<String>),
    /// The socket closed. The loop dials again.
    Detached,
}

/// One line mpv wrote.
#[derive(Debug, Clone, PartialEq)]
pub enum Incoming {
    Reply { id: u64, data: Value },
    Event(Event),
}

/// One command as the line mpv's socket takes: newline-delimited JSON,
/// carrying the request id the reply comes back under.
pub fn command(id: u64, arguments: Vec<Value>) -> String {
    format!("{}\n", json!({ "command": arguments, "request_id": id }))
}

/// The command that asks mpv to push one property now and on every change.
pub fn observe(id: u64, name: &str) -> String {
    command(id, vec![json!("observe_property"), json!(id), json!(name)])
}

/// The command that reads one property once.
pub fn get_property(id: u64, name: &str) -> String {
    command(id, vec![json!("get_property"), json!(name)])
}

/// One line mpv wrote, as the display reads it. A line that is not JSON, and
/// an event this display has no case for, read as nothing.
pub fn decode(line: &str) -> Option<Incoming> {
    let message: Value = serde_json::from_str(line).ok()?;
    match message.get("event").and_then(Value::as_str) {
        Some("property-change") => {
            let name = message.get("name")?.as_str()?.to_string();
            let value = message.get("data").cloned().unwrap_or(Value::Null);
            Some(Incoming::Event(Event::Property { name, value }))
        }
        Some("client-message") => {
            let arguments = message
                .get("args")?
                .as_array()?
                .iter()
                .map(|argument| match argument {
                    Value::String(word) => word.clone(),
                    other => other.to_string(),
                })
                .collect();
            Some(Incoming::Event(Event::Message(arguments)))
        }
        Some(_) => None,
        None => {
            let id = message.get("request_id")?.as_u64()?;
            let data = message.get("data").cloned().unwrap_or(Value::Null);
            Some(Incoming::Reply { id, data })
        }
    }
}

/// Whether one `client-message` summons the display. The `summon` message
/// says so outright, and every navigation action wakes a hidden display as
/// well.
pub fn summons(arguments: &[String]) -> bool {
    arguments
        .first()
        .is_some_and(|word| word == SUMMON || ACTIONS.contains(&word.as_str()))
}

/// The mpv socket the display reads.
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
    /// The socket the environment names, or the pod's own.
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

    /// The socket this client dials. It names the subscription that reads
    /// the socket, so the frame loop opens one reader and no more.
    pub fn path(&self) -> &Path {
        &self.path
    }

    /// Wait until mpv answers, reports a position, and has its video output
    /// up. The display opens its window after that, so its surface arrives
    /// after mpv's and the compositor's controller, which stacks a claim's
    /// surfaces newest on top, puts the display above the film.
    ///
    /// The wait ends at the deadline whatever the socket does, so a player
    /// that never starts leaves the container to exit rather than stand
    /// windowless.
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

    /// Dial the socket, observe every property, and send on until the
    /// process ends. A socket that closes reports `Detached` and the loop
    /// dials again, because mpv restarting is the pod's own business and the
    /// display outlives it.
    pub async fn serve(&self, events: Sender<Event>) {
        loop {
            match tokio::time::timeout(DIAL_TIMEOUT, UnixStream::connect(&self.path)).await {
                Ok(Ok(stream)) => {
                    let _ = session(stream, &events).await;
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

/// One attachment: observe every property, then read lines until the socket
/// closes.
///
/// The read of an attached socket carries no deadline, while every dial
/// does. A paused film writes nothing for as long as it stands, so silence
/// on an attached socket is not a failure. The socket closing is, and that
/// is what ends the session.
pub async fn session<S>(stream: S, events: &Sender<Event>) -> io::Result<()>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let (reader, mut writer) = tokio::io::split(stream);
    for (index, name) in OBSERVED.iter().enumerate() {
        writer
            .write_all(observe(index as u64 + 1, name).as_bytes())
            .await?;
    }

    let mut lines = BufReader::new(reader).lines();
    while let Some(line) = lines.next_line().await? {
        if let Some(Incoming::Event(event)) = decode(&line)
            && events.send(event).await.is_err()
        {
            return Ok(());
        }
    }
    Ok(())
}

/// Ask for both gate properties once and answer whether mpv is playing with
/// its video output up.
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
            Some(Incoming::Reply { id, data }) if id == GATE[0].0 => {
                playing = Some(data.is_number());
            }
            Some(Incoming::Reply { id, data }) if id == GATE[1].0 => {
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
            })
        );
        assert_eq!(
            decode(r#"{"data":null,"request_id":1,"error":"property unavailable"}"#),
            Some(Incoming::Reply {
                id: 1,
                data: Value::Null,
            })
        );
    }

    #[test]
    fn a_line_this_display_has_no_case_for_reads_as_nothing() {
        assert_eq!(decode("not json at all"), None);
        assert_eq!(decode(r#"{"event":"seek"}"#), None);
        assert_eq!(decode(r#"{"event":"property-change","id":1}"#), None);
        assert_eq!(decode(r#"{"event":"client-message"}"#), None);
        assert_eq!(decode("{}"), None);
    }

    #[test]
    fn the_summon_and_the_six_actions_show_the_display() {
        assert!(summons(&["summon".to_string()]));
        for action in ACTIONS {
            assert!(summons(&[action.to_string()]), "{action} summons");
        }
        assert!(!summons(&["liken-art".to_string()]));
        assert!(!summons(&["presentation".to_string(), "{}".to_string()]));
        assert!(!summons(&[]));
    }

    /// The session opens on one half of a pipe and mpv's half reads back
    /// every observe the display sent, in the order the list names them.
    #[tokio::test]
    async fn a_session_observes_every_property_the_display_reads() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, _events) = mpsc::channel(64);
        let session = tokio::spawn(async move {
            let _ = session(display, &sender).await;
        });

        let mut read = vec![0u8; 4096];
        let mut sent = String::new();
        while sent.lines().count() < OBSERVED.len() {
            let count = mpv.read(&mut read).await.expect("mpv reads the observes");
            sent.push_str(&String::from_utf8_lossy(&read[..count]));
        }
        let lines: Vec<&str> = sent.lines().collect();
        assert_eq!(lines.len(), OBSERVED.len());
        for (index, name) in OBSERVED.iter().enumerate() {
            assert_eq!(lines[index], observe(index as u64 + 1, name).trim_end());
        }

        drop(mpv);
        session.await.expect("the session ends with the socket");
    }

    /// Every line mpv writes on an attached socket reaches the display as an
    /// event, and the stream ends when the socket closes.
    #[tokio::test]
    async fn a_session_carries_every_event_mpv_writes() {
        let (display, mut mpv) = tokio::io::duplex(8192);
        let (sender, mut events) = mpsc::channel(64);
        let session = tokio::spawn(async move {
            let _ = session(display, &sender).await;
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
        .expect("mpv writes its events");

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
        session.await.expect("the session ends with the socket");
        assert_eq!(events.recv().await, None);
    }

    /// The window gate asks for both properties and opens the window only
    /// when the film is playing and mpv's video output is up.
    #[tokio::test]
    async fn the_gate_holds_until_the_film_plays_and_the_output_is_up() {
        for (position, output, want) in [
            ("12.5", "true", true),
            ("null", "true", false),
            ("12.5", "false", false),
            ("null", "false", false),
        ] {
            let (display, mut mpv) = tokio::io::duplex(8192);
            let gate = tokio::spawn(showing(display));

            let mut read = vec![0u8; 512];
            let count = mpv.read(&mut read).await.expect("mpv reads the requests");
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
            .expect("mpv answers");

            assert_eq!(gate.await.expect("the gate ends").ok(), Some(want));
        }
    }

    /// A socket nothing listens on never answers, and the gate gives up at
    /// its deadline rather than waiting on a player that never starts.
    #[tokio::test]
    async fn the_gate_gives_up_at_its_deadline() {
        let ipc = Ipc::at("/nonexistent/mpv.sock");
        assert!(!ipc.playing(Duration::from_millis(1)).await);
    }

    /// The loop attaches, carries what mpv writes, reports the socket
    /// closing, and dials again.
    #[tokio::test]
    async fn the_loop_attaches_reports_the_close_and_dials_again() {
        let dir = std::env::temp_dir().join(format!("media-osd-serve-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("a directory for the socket");
        let path = dir.join("mpv.sock");
        let listener = tokio::net::UnixListener::bind(&path).expect("a socket to dial");

        let ipc = Ipc::at(&path);
        let (sender, mut events) = mpsc::channel(64);
        let serving = tokio::spawn(async move { ipc.serve(sender).await });

        let (mut mpv, _) = listener.accept().await.expect("the display dials");
        let mut read = vec![0u8; 4096];
        let mut sent = String::new();
        while sent.lines().count() < OBSERVED.len() {
            let count = mpv.read(&mut read).await.expect("mpv reads the observes");
            sent.push_str(&String::from_utf8_lossy(&read[..count]));
        }
        mpv.write_all(b"{\"event\":\"client-message\",\"args\":[\"summon\"]}\n")
            .await
            .expect("mpv writes one message");
        assert_eq!(
            events.recv().await,
            Some(Event::Message(vec!["summon".to_string()]))
        );

        drop(mpv);
        assert_eq!(events.recv().await, Some(Event::Detached));

        tokio::time::timeout(Duration::from_secs(5), listener.accept())
            .await
            .expect("the loop dials again")
            .expect("the display dials again");

        serving.abort();
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// The loop ends when the frame loop stops reading, so a display that
    /// closed its window leaves no reader behind.
    #[tokio::test]
    async fn the_loop_ends_when_the_display_stops_reading() {
        let ipc = Ipc::at("/nonexistent/mpv.sock");
        let (sender, events) = mpsc::channel(1);
        drop(events);

        tokio::time::timeout(Duration::from_secs(5), ipc.serve(sender))
            .await
            .expect("the loop ends");
    }

    #[test]
    fn the_socket_is_the_one_the_command_sidecar_drives() {
        assert_eq!(SOCKET_PATH, "/ipc/mpv.sock");
        assert_eq!(Ipc::default().path, PathBuf::from(SOCKET_PATH));
    }

    /// The tests read one process environment, so this one runs alone and
    /// puts back what it found.
    #[test]
    fn a_named_socket_wins_over_the_pod_path() {
        let found = std::env::var(SOCKET_VARIABLE).ok();

        // SAFETY: the crate's tests set this variable in one test alone.
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
