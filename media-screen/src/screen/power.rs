//! Handles the power topic, where `media-operator` requests that the screen
//! wake or sleep. `media-operator` publishes `wake` when the `Player`'s
//! `spec.power` turns on and `sleep` when it turns off. It also relays
//! requests from a TV over HDMI-CEC: `wake` when a person selects this
//! `Player`'s input, and `sleep` when the TV goes to standby.
//! `equipment-operator`'s CEC workload has no broker connection, so it writes
//! the request to the `Television`'s `status.screenAsk`, and
//! `media-operator/screenask.go` publishes it here.
//!
//! Each request sets the panel request, as a key press or a timer does.
//! `media-operator` copies `spec.power` and the panel request into the
//! `awake` field of the `Player`'s session on its `Receiver`.
//! `equipment-operator` sends the A/V receiver power and input commands for
//! the session only while `awake` is true or a `Play` exists.

use std::time::Instant;

use serde::Deserialize;

use super::{Effect, Moment, Screen};
use crate::panel;

const WAKE: &str = "wake";

const SLEEP: &str = "sleep";

#[derive(Deserialize)]
struct PowerMessage {
    #[serde(default)]
    action: String,
}

impl Screen {
    /// A message with any other action, or one that is not a JSON object,
    /// changes nothing.
    pub(super) fn on_power(&mut self, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(message) = crate::object::<PowerMessage>(payload) else {
            return Vec::new();
        };
        match message.action.as_str() {
            WAKE => self.wake_on_request(now),
            SLEEP => self.sleep_on_request(now),
            _ => Vec::new(),
        }
    }

    /// Wakes a sleeping screen and requests the panel on, as a key press on a
    /// sleeping screen does. On a screen that is already awake with its panel
    /// requested on, it only restarts the fade timer.
    fn wake_on_request(&mut self, now: Instant) -> Vec<Effect> {
        let moment = self.asleep.then_some(Moment::Wake);
        self.asleep = false;
        self.rearm(now);
        let mut effects = Vec::new();
        let mut panel_line = self.push_sleep_or_wake(moment, &mut effects);
        if panel_line.is_empty() {
            panel_line = self.request_panel(panel::ON, &mut effects);
        }
        if !effects.is_empty() {
            self.lines.push(format!(
                "{} requested wake, so the screen is awake{panel_line}",
                self.power_topic
            ));
        }
        effects
    }

    /// Puts the screen to sleep and requests the panel off at once, so the
    /// panel goes dark with the TV instead of when the off timer expires.
    /// Like the fade timer, it does nothing while the `Player` is not idle.
    fn sleep_on_request(&mut self, now: Instant) -> Vec<Effect> {
        if !self.idle {
            return Vec::new();
        }
        let moment = (!self.asleep).then_some(Moment::Sleep);
        self.asleep = true;
        let mut effects = Vec::new();
        self.push_sleep_or_wake(moment, &mut effects);
        let panel_line = self.request_panel(panel::OFF, &mut effects);
        self.rearm(now);
        if !effects.is_empty() {
            self.lines.push(format!(
                "{} requested sleep, so the screen is asleep{panel_line}",
                self.power_topic
            ));
        }
        effects
    }
}
