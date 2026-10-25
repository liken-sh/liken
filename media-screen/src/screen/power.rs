//! The power topic's asks for the screen. `media-operator` owns the
//! `Player`'s power in `spec.power`, and it publishes two asks on this
//! topic: sleep when the `Player` turns off, and wake when it turns on.
//! It publishes the same two asks when a TV speaks for the screen over
//! HDMI-CEC: wake when a person picks this unit's input in the TV's
//! source menu, and sleep when the TV goes to standby. The equipment
//! operator's CEC node workload holds no broker connection, so it writes
//! that ask in the Television's `status.screenAsk`, and `media-operator`
//! publishes it here (`screenask.go`).
//!
//! The screen answers each ask the way a press or the quiet window would.
//! The panel desire moves with it, and the `Receiver` session's awake
//! flag, which `media-operator` derives from the desire, moves with the
//! panel.

use std::time::Instant;

use serde::Deserialize;

use super::{Effect, Moment, Screen};
use crate::panel;

/// The ask to wake the screen and ask for its panel.
const WAKE: &str = "wake";

/// The ask to lower the shade and turn the panel off.
const SLEEP: &str = "sleep";

/// The one field of the power topic this crate reads.
#[derive(Deserialize)]
struct Ask {
    #[serde(default)]
    action: String,
}

impl Screen {
    /// Fold one message off the power topic. An action other than wake and
    /// sleep is not this crate's, and it changes nothing.
    pub(super) fn on_power(&mut self, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(ask) = crate::object::<Ask>(payload) else {
            return Vec::new();
        };
        match ask.action.as_str() {
            WAKE => self.wake_asked(now),
            SLEEP => self.sleep_asked(now),
            _ => Vec::new(),
        }
    }

    /// Wake a sleeping screen and state the on desire, the way a press on a
    /// sleeping screen does. A screen that is awake with its panel on has
    /// nothing to do.
    fn wake_asked(&mut self, now: Instant) -> Vec<Effect> {
        let moment = self.asleep.then_some(Moment::Wake);
        self.asleep = false;
        self.rearm(now);
        let mut effects = Vec::new();
        let mut desire = self.shade(moment, &mut effects);
        if desire.is_empty() {
            desire = self.desire(panel::ON, &mut effects);
        }
        if !effects.is_empty() {
            self.lines.push(format!(
                "{} asked to wake the screen{desire}",
                self.power_topic
            ));
        }
        effects
    }

    /// Lower the shade and state the off desire at once, so the screen goes
    /// dark with the equipment and not after the off window. It acts only
    /// while the unit plays nothing: a film goes on, the same rule the quiet
    /// window follows.
    fn sleep_asked(&mut self, now: Instant) -> Vec<Effect> {
        if !self.idle {
            return Vec::new();
        }
        let moment = (!self.asleep).then_some(Moment::Sleep);
        self.asleep = true;
        let mut effects = Vec::new();
        self.shade(moment, &mut effects);
        let desire = self.desire(panel::OFF, &mut effects);
        self.rearm(now);
        if !effects.is_empty() {
            self.lines.push(format!(
                "{} asked the screen to sleep, so the shade is down{desire}",
                self.power_topic
            ));
        }
        effects
    }
}
