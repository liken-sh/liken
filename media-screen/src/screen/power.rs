//! The power topic's asks for the screen. A unit whose screen is wired
//! through a Receiver publishes the room's power on the power topic, and
//! the equipment operator publishes two asks back on the same topic when
//! the TV speaks for the room over HDMI-CEC: wake when a person picks this
//! unit's input in the TV's source menu while the screen sleeps, and sleep
//! when the TV goes to standby while the screen is awake. The screen
//! answers each the way a press or the quiet window would, so the panel
//! desire, and the Receiver session that follows it, move with the TV.
//!
//! The operator relays the two asks only to a unit with a `Receiver`. A
//! client in the screen mode still ignores them, so an ask that was in flight
//! when the `Receiver` was removed does not move a screen that handles power
//! itself.

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
    /// Fold one message off the power topic. Every action but wake and
    /// sleep is an ask for the equipment operator, such as this screen's own
    /// toggle coming back, and changes nothing here.
    pub(super) fn on_power(&mut self, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(ask) = crate::object::<Ask>(payload) else {
            return Vec::new();
        };
        let asked = match ask.action.as_str() {
            WAKE => "wake",
            SLEEP => "sleep",
            _ => return Vec::new(),
        };
        if !self.room_power() {
            self.lines.push(format!(
                "{} asked the screen to {asked}, ignored, because the power mode is screen",
                self.power_topic
            ));
            return Vec::new();
        }
        if asked == WAKE {
            self.wake_asked(now)
        } else {
            self.sleep_asked(now)
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

    /// Lower the shade and state the off desire at once, so the room goes
    /// dark with the TV and not after the off window. It acts only while the
    /// unit plays nothing: a film goes on, the same rule the quiet window
    /// follows.
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
