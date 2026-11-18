//! The commands topic, on which the command sidecar of the playback pod
//! publishes `play-next` and `home` for the client.

use serde::Deserialize;

use super::{Effect, Moment, Screen, keys};

/// Published when a person accepts the up-next offer during a film. The
/// client that wrote the `Play` starts the next item.
const PLAY_NEXT: &str = "play-next";

/// Published when a person presses home during a film, just before the
/// `Play` ends.
const HOME: &str = "home";

/// `request` is whatever JSON the writer of the `Play` put there, and this
/// crate passes it on without reading it.
#[derive(Deserialize)]
struct Command {
    #[serde(default)]
    action: String,
    #[serde(default)]
    request: Option<serde_json::Value>,
}

fn request_bytes(request: Option<serde_json::Value>) -> Vec<u8> {
    let Some(value) = request else {
        return Vec::new();
    };
    serde_json::to_vec(&value).unwrap_or_default()
}

impl Screen {
    /// Passes `play-next` and `home` to the client even though the `Player`
    /// is not idle, because both arrive while a film plays. `home` arrives as a
    /// press of `keys::HOME`, so the client handles it like the home key of
    /// a remote.
    pub(super) fn on_command(&mut self, payload: &[u8]) -> Vec<Effect> {
        let Some(command) = crate::object::<Command>(payload) else {
            return Vec::new();
        };
        match command.action.as_str() {
            PLAY_NEXT => {
                self.lines.push(format!(
                    "{} received {PLAY_NEXT}, passed to the client",
                    self.commands_topic
                ));
                vec![Effect::Moment(Moment::PlayNext(request_bytes(
                    command.request,
                )))]
            }
            HOME => {
                self.lines.push(format!(
                    "{} received {HOME}, passed to the client as {}",
                    self.commands_topic,
                    keys::HOME
                ));
                vec![Effect::Moment(Moment::Press(keys::HOME.into()))]
            }
            _ => Vec::new(),
        }
    }
}
