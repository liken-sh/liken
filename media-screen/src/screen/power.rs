//! The power topic's asks for the screen. A unit whose screen is wired
//! through a Receiver publishes the room's power on the power topic, and
//! `media-operator` writes each of those asks into the Receiver's
//! `status.session.powerAsk` (`roompower.go`). Two asks come back on the
//! same topic when the TV speaks for the room over HDMI-CEC: wake when a
//! person picks this unit's input in the TV's source menu while the screen
//! sleeps, and sleep when the TV goes to standby while the screen is awake.
//! The equipment operator's CEC node workload holds no broker connection,
//! so it writes each ask in the Television's `status.screenAsk`, and
//! `media-operator` publishes it on the power topic (`screenask.go`). The
//! screen answers each the way a press or the quiet window would, so the
//! panel desire, and the Receiver session that follows it, move with the
//! TV.
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

/// The power asks that name the state they want.
const OFF: &str = "off";
const ON: &str = "on";

/// The one field of the power topic this crate reads.
#[derive(Deserialize)]
struct Ask {
    #[serde(default)]
    action: String,
}

impl Screen {
    /// Fold one message off the power topic. Every action but wake and
    /// sleep is a room power ask for `media-operator`, such as this screen's
    /// own toggle coming back, and changes nothing here.
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

    /// Turn the Player off or on for a power key, or for a power ask the
    /// playback pod held until the unit was idle. The panel desire is the
    /// Player's power, so it moves with the ask: the Receiver session's
    /// awake flag follows the desire, and a desire that stood on after a
    /// power-off would leave the session awake with its equipment off,
    /// where no press could wake it again until the off window ran.
    ///
    /// Off publishes the off ask first, so the equipment operator reads the
    /// standby before the awake flag turns false, then lowers the shade and
    /// states the off desire. On wakes the screen and states the on desire,
    /// and the awake flag that turns true wakes the TV and the receiver. A
    /// Player that was already lit flips no flag, so on publishes the on
    /// ask for equipment that someone turned off another way. The toggle
    /// turns a lit Player off and a dark one on. A client that read no
    /// desire does not know which, so it publishes the toggle and the
    /// equipment operator decides from the TV's power.
    ///
    /// The answer is the end of the line that the caller writes.
    pub(super) fn turn_power(
        &mut self,
        action: &'static str,
        now: Instant,
        effects: &mut Vec<Effect>,
    ) -> String {
        let off = match (action, self.desire) {
            (OFF, _) => true,
            (ON, _) => false,
            (_, Some(desire)) => desire == panel::ON,
            (_, None) => {
                effects.push(Effect::Publish(self.power_publish(action)));
                return format!("published the toggle to {}", self.power_topic);
            }
        };
        if off {
            effects.push(Effect::Publish(self.power_publish(OFF)));
            let moment = (!self.asleep).then_some(Moment::Sleep);
            self.asleep = true;
            self.shade(moment, effects);
            let desire = self.desire(panel::OFF, effects);
            self.rearm(now);
            return format!("published off to {}{desire}", self.power_topic);
        }
        let lit = self.desire == Some(panel::ON);
        let moment = self.asleep.then_some(Moment::Wake);
        self.asleep = false;
        self.rearm(now);
        let mut desire = self.shade(moment, effects);
        if desire.is_empty() {
            desire = self.desire(panel::ON, effects);
        }
        if lit {
            effects.push(Effect::Publish(self.power_publish(ON)));
            return format!("published on to {}{desire}", self.power_topic);
        }
        format!("woke the screen{desire}")
    }
}
