//! The commands topic: the asks the playback pod's command sidecar
//! publishes for the client under the film. The up-next ask and the home
//! ask pass to the client at once. The power ask waits for the unit's
//! `Idle` status, because the equipment operator decides off or on from
//! the room it reads, and a toggle that went out while the `Play` still
//! ran would reach the room before the ending did.

use std::time::{Duration, Instant};

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

/// The ask the same sidecar publishes when a person presses power during a
/// film, just before the `Play` ends.
const POWER: &str = "power";

/// The ask the same sidecar publishes when a TV remote's Power Off Function
/// reaches it during a film, just before the `Play` ends. It is answered as
/// a press of [`keys::POWER_OFF`], so a room already off stays off.
const POWER_OFF: &str = "power-off";

/// The key each held power ask is answered as.
fn ask_key(ask: &str) -> &'static str {
    if ask == POWER_OFF {
        keys::POWER_OFF
    } else {
        keys::POWER_PRESS
    }
}

/// How long a power ask waits for the unit's `Idle` status. The sidecar
/// publishes the ending just after the ask, and the operator publishes
/// `Idle` when it reads the ending, so the wait is normally short. An ask
/// that outlives this deadline has no ending behind it, for example
/// because the sidecar stopped before it published one. It is dropped,
/// because a toggle long after the press would turn the room off or on
/// with no person behind it.
const POWER_ASK_WAIT: Duration = Duration::from_secs(10);

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
    ///
    /// A power ask while the unit plays is held until the status reads
    /// `Idle`, and a power ask while the unit is idle is answered at once.
    pub(super) fn on_command(&mut self, payload: &[u8], now: Instant) -> Vec<Effect> {
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
            ask @ (POWER | POWER_OFF) if self.idle => {
                let mut effects = Vec::new();
                self.answer_power_ask(ask_key(ask), &mut effects);
                effects
            }
            ask @ (POWER | POWER_OFF) => {
                // A second ask while one waits restarts the deadline and
                // is still one ask, so the room toggles once. The later
                // ask names the key it is answered as.
                self.power_ask = Some((now + POWER_ASK_WAIT, ask_key(ask)));
                self.lines.push(format!(
                    "{} asked for {ask} while the player plays, held until it is Idle for at most {} s",
                    self.commands_topic,
                    POWER_ASK_WAIT.as_secs()
                ));
                Vec::new()
            }
            _ => Vec::new(),
        }
    }

    /// Answer a power ask the way a press of its key answers while the unit
    /// is idle. With a Receiver, the key's ask goes out on the power topic,
    /// and the shade and the panel desire stand, for the reason the press's
    /// power branch gives. With none, the client reads the ask as a press of
    /// the key and lowers its shade.
    pub(super) fn answer_power_ask(&mut self, key: &'static str, effects: &mut Vec<Effect>) {
        let ask = if key == keys::POWER_OFF {
            POWER_OFF
        } else {
            POWER
        };
        let asked = format!(
            "{} asked for {ask} and the player is Idle",
            self.commands_topic
        );
        let action = keys::power_action(key).unwrap_or("toggle");
        if self.power_topic.is_empty() {
            self.lines
                .push(format!("{asked}, so passed to the client as {key}"));
            effects.push(Effect::Moment(Moment::Press(key.into())));
            return;
        }
        let what = if action == "toggle" {
            "the toggle".to_string()
        } else {
            action.to_string()
        };
        self.lines.push(format!(
            "{asked}, so published {what} to {}",
            self.power_topic
        ));
        effects.push(Effect::Publish(self.power_publish(action)));
    }

    /// Drop a held power ask whose deadline passed. The deadline is a clock,
    /// not a poll: the reader's clock thread wakes at
    /// [`Screen::next_deadline`], and no status is read again.
    pub(super) fn expire_power_ask(&mut self, now: Instant) {
        let Some((at, key)) = self.power_ask else {
            return;
        };
        if now < at {
            return;
        }
        self.power_ask = None;
        let ask = if key == keys::POWER_OFF {
            POWER_OFF
        } else {
            POWER
        };
        self.lines.push(format!(
            "{} asked for {ask}, and no Idle status arrived within {} s, so the ask is dropped",
            self.commands_topic,
            POWER_ASK_WAIT.as_secs()
        ));
    }
}
