//! The commands topic: the asks the playback pod's command sidecar
//! publishes for the client under the film. The up-next ask and the home
//! ask pass to the client at once.

use serde::Deserialize;

use super::{Effect, Moment, Screen, keys};

/// The ask the playback pod's command sidecar publishes when a person takes
/// the up-next offer on the scrubber. The client that wrote the `Play` reads
/// it and starts what follows.
const PLAY_NEXT: &str = "play-next";

/// The ask the same sidecar publishes when a person presses home during a
/// film. The client reads it as a press of the home key, just before the
/// `Play` ends.
const HOME: &str = "home";

/// The two fields of the commands topic this crate reads. The request is
/// whatever object the writer of the `Play` put there, so it is kept as a
/// value and passed on unread.
#[derive(Deserialize)]
struct Command {
    #[serde(default)]
    action: String,
    #[serde(default)]
    request: Option<serde_json::Value>,
}

/// The request as bytes, for a client that parses it with its own types. A
/// message that carries none gives an empty request.
fn request_bytes(request: Option<serde_json::Value>) -> Vec<u8> {
    let Some(value) = request else {
        return Vec::new();
    };
    serde_json::to_vec(&value).unwrap_or_default()
}

impl Screen {
    /// Fold one message off the commands topic. The ask a person makes on the
    /// up-next offer acts whether or not the unit is idle, because the unit is
    /// never idle when it arrives.
    ///
    /// A home ask reaches the client as a press of the home key, so the
    /// client binds one name for home.
    pub(super) fn on_command(&mut self, payload: &[u8]) -> Vec<Effect> {
        let Some(command) = crate::object::<Command>(payload) else {
            return Vec::new();
        };
        match command.action.as_str() {
            PLAY_NEXT => {
                self.lines.push(format!(
                    "{} asked for {PLAY_NEXT}, passed to the client",
                    self.commands_topic
                ));
                vec![Effect::Moment(Moment::PlayNext(request_bytes(
                    command.request,
                )))]
            }
            HOME => {
                self.lines.push(format!(
                    "{} asked for {HOME}, passed to the client as {}",
                    self.commands_topic,
                    keys::HOME
                ));
                vec![Effect::Moment(Moment::Press(keys::HOME.into()))]
            }
            _ => Vec::new(),
        }
    }
}
