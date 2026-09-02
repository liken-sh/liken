//! The rules a screen client holds for one `Player`: the quiet window and the
//! off window, the focus gate, the shade, the level a press steps, the cycle
//! request, and the panel desire.
//!
//! [`Screen`] reaches no socket and holds no clock. Every rule below is
//! a function of what arrived and what time it is, so a test proves
//! each one with no broker and no thread, and [`crate::Reader`] is the
//! only part of the crate that opens anything.
//!
//! The re-present is the whole fix for a seatless compositor. Weston's
//! kiosk-shell reveals a lower surface only along a code path gated on a
//! seat, and `liken`'s compositor runs with require-input=false and no input
//! devices, so it has no seat. When a `Play`'s surface is destroyed the idle
//! clock stays hidden and the screen goes black, though the client still
//! runs. A freshly mapped surface is revealed along a seat-independent path,
//! so the operator publishes the request, the client maps a new surface, and
//! kiosk reveals that one.

pub mod keys;
pub mod press;

use std::time::{Duration, Instant};

use serde::Deserialize;

use crate::panel;
use crate::status::{Activity, Status};
use crate::volume::Volume;
use crate::wiring::{Remote, Wiring};

/// The suffix that turns a remote's focus topic into its cycle topic, the
/// same path `media-operator`'s `remoteFocusCycleTopic` builds, so a client
/// needs no second topic list.
const CYCLE_SUFFIX: &str = "/cycle";

/// The one command this crate answers on the `Player`'s commands topic.
const RE_PRESENT: &str = "re-present";

/// One thing the client draws.
///
/// Each one is a fact the screen shows or a moment it moves on, and
/// none of them is a decision the client makes again. The shade moments
/// say which way the cover eases. A focus names the controller a live
/// mark landed on, by its place in `spec.remotes`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Moment {
    /// One navigation press, under the kernel's name for the control. The
    /// client holds its own table from these names to what they do there.
    Press(&'static str),
    /// The quiet window ran out, or the client asked for the shade.
    Sleep,
    /// A press, a live mark, or a starting `Play` lifted the shade.
    Wake,
    /// A live mark named this `Player`. `remote` is the controller's place in
    /// `spec.remotes`, which is the order the status lists the parts in.
    Focus { remote: usize },
    /// A `Play` ended and the screen is this client's again. The client maps
    /// a fresh Wayland surface, and kiosk reveals that one.
    Present,
    /// The unit's whole presentable state.
    Status(Status),
    /// The unit's listening level. `pressed` is false for the broker's
    /// catch-up and true for a press.
    Level { volume: Volume, pressed: bool },
}

/// One message this crate sends on the bus. The client never builds one:
/// [`crate::Reader`] performs each of these on the connection it already
/// holds.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Publish {
    pub topic: String,
    pub payload: Vec<u8>,
    pub retained: bool,
}

/// What one fold leaves to do: something the client draws, or something the
/// crate sends.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Effect {
    Moment(Moment),
    Publish(Publish),
}

/// One controller's mark and whether this bus session already delivered one.
/// The first message of a session is the broker's retained catch-up, a
/// restore and not a person, so it sets the gate and pulses nothing.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
struct Mark {
    player: String,
    caught_up: bool,
}

/// Which window the armed deadline belongs to.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Window {
    /// The quiet window, which brings the shade down.
    Quiet,
    /// The off window, which states the off desire.
    Off,
}

/// The state a screen client holds for one unit.
#[derive(Debug)]
pub struct Screen {
    /// The `Player`'s own object name, the value a focus mark holds when it
    /// names this unit. An empty name matches no mark, so a client that read
    /// none answers no press.
    player_name: String,
    status_topic: String,
    /// The unit's volume topic, empty for a `Player` with no sinks. Empty is
    /// the speaker gate: the client subscribes to no level and answers no
    /// volume press.
    volume_topic: String,
    commands_topic: String,
    panel_topic: String,
    /// The unit's controllers, in `spec.remotes` order, so a controller's
    /// index in this list is the index a focus moment carries.
    remotes: Vec<Remote>,
    /// The last mark each controller's focus topic delivered, one per entry
    /// of `remotes`.
    marks: Vec<Mark>,

    /// The quiet window. Zero never arms the timer.
    fade_after: Duration,
    /// The off window, clamped to at least the fade. Zero leaves the desire
    /// at on forever.
    off_after: Duration,

    /// The last state the volume topic delivered. A press steps from it, and
    /// from unity before any message arrives.
    volume: Option<Volume>,
    /// Whether the last status named the activity `Idle`, the only state the
    /// timer arms in. It starts false, so a client answers no press until the
    /// retained status reaches it.
    idle: bool,
    /// Whether the shade is down.
    asleep: bool,
    /// The panel state this client last stated, on or off. The client writes
    /// no hardware; the operator reads the desire from the bus and overrides
    /// the screen's `Display`.
    desire: &'static str,
    /// The armed window and the moment it runs out.
    deadline: Option<(Instant, Window)>,
}

/// The one field of the commands topic this crate reads.
#[derive(Deserialize)]
struct Command {
    #[serde(default)]
    action: String,
}

impl Screen {
    /// The screen one wiring describes.
    pub fn new(wiring: &Wiring) -> Self {
        Self {
            player_name: wiring.player_name.clone(),
            status_topic: wiring.status_topic.clone(),
            volume_topic: wiring.volume_topic.clone(),
            commands_topic: wiring.commands_topic.clone(),
            panel_topic: wiring.panel_topic.clone(),
            marks: vec![Mark::default(); wiring.remotes.len()],
            remotes: wiring.remotes.clone(),
            fade_after: wiring.fade_after,
            off_after: wiring.off_after,
            volume: None,
            idle: false,
            asleep: false,
            // A client that starts holds the on desire, and states it on its
            // first bus session, so a pod that returns while the panel is
            // dark lights it again.
            desire: panel::ON,
            deadline: None,
        }
    }

    /// The topics to subscribe to. An empty topic is one the operator did not
    /// set, and a unit with no sinks has no volume topic at all.
    ///
    /// A press on any of the unit's controllers reaches the quiet window, so
    /// every events topic is read. The focus topic is retained, so each mark
    /// arrives on subscribe and the gate stands before the first press.
    pub fn filters(&self) -> Vec<String> {
        let mut filters = vec![
            self.status_topic.clone(),
            self.volume_topic.clone(),
            self.commands_topic.clone(),
        ];
        for remote in &self.remotes {
            filters.push(remote.events.clone());
            filters.push(remote.focus.clone());
        }
        filters.retain(|topic| !topic.is_empty());
        filters
    }

    /// The moment the armed window runs out, and nothing while none is armed.
    pub fn next_deadline(&self) -> Option<Instant> {
        self.deadline.map(|(at, _)| at)
    }

    /// Fold one message from any subscription, and the topic says which
    /// message this is. A topic this screen did not subscribe to, and a
    /// payload that does not decode, are both nothing at all, so a newer
    /// message on a topic has no effect rather than a crash.
    ///
    /// `retained` is the broker's own mark on a delivery from its retained
    /// store. A retained level is the catch-up, which a person did not press,
    /// so it sets the level and shows no indicator; a live level is a press.
    pub fn deliver(
        &mut self,
        topic: &str,
        payload: &[u8],
        retained: bool,
        now: Instant,
    ) -> Vec<Effect> {
        if !self.status_topic.is_empty() && topic == self.status_topic {
            return self.on_status(payload, now);
        }
        // The volume topic carries a state and not a named command, so it is
        // read before the command vocabulary below.
        if !self.volume_topic.is_empty() && topic == self.volume_topic {
            return self.on_level(payload, retained);
        }
        // A controller's presses are checked before the commands topic,
        // because a key event is not the operator's command vocabulary.
        if let Some(index) = self.remote_for(topic, |remote| &remote.events) {
            return self.on_press(index, payload, now);
        }
        // The mark is a state on its own retained topic, read before the
        // command vocabulary for the same reason the level is.
        if let Some(index) = self.remote_for(topic, |remote| &remote.focus) {
            return self.on_focus(index, payload, now);
        }
        if !self.commands_topic.is_empty() && topic == self.commands_topic {
            return self.on_command(payload);
        }
        Vec::new()
    }

    /// The armed window running out: the quiet window brings the shade down
    /// and starts the off window, and the off window states the off desire
    /// and arms nothing.
    pub fn tick(&mut self, now: Instant) -> Vec<Effect> {
        let Some((at, window)) = self.deadline else {
            return Vec::new();
        };
        if now < at {
            return Vec::new();
        }
        let mut effects = Vec::new();
        match window {
            Window::Quiet => {
                self.asleep = true;
                // The shade coming down starts the second window.
                self.rearm(now);
                self.shade(Some(Moment::Sleep), &mut effects);
            }
            Window::Off => {
                self.deadline = None;
                self.desire(panel::OFF, &mut effects);
            }
        }
        effects
    }

    /// Bring the shade down on the client's own reading of a press. The stock
    /// idle client asks on back; a client with levels asks only at the top
    /// one, because only the client knows whether back has anywhere to go.
    ///
    /// It acts only while the unit plays nothing and the screen is awake, and
    /// it makes the same three moves the quiet window makes, so the shade and
    /// the panel desire behave the same whichever one asked.
    pub fn sleep(&mut self, now: Instant) -> Vec<Effect> {
        if !self.idle || self.asleep {
            return Vec::new();
        }
        self.asleep = true;
        self.rearm(now);
        let mut effects = Vec::new();
        self.shade(Some(Moment::Sleep), &mut effects);
        effects
    }

    /// The start of every bus session. A fresh session redelivers every
    /// retained mark, so each one is a catch-up again and pulses nothing. The
    /// mark itself stands across the reconnect, so the gate does not open or
    /// close on a broker restart alone.
    ///
    /// The panel desire is this client's own retained state, so it goes out
    /// again on every session. A client that returns while the panel is dark
    /// states the on desire here, and the operator lifts the override.
    pub fn connected(&mut self) -> Vec<Effect> {
        for mark in &mut self.marks {
            mark.caught_up = false;
        }
        let mut effects = Vec::new();
        self.publish_desire(&mut effects);
        effects
    }

    /// Fold one status. `Idle` is the only activity the timer arms in, so a
    /// status that leaves `Idle` disarms it. The same status lifts the shade
    /// if the screen sleeps, so a `Play` started from another room shows its
    /// film and not a black screen.
    ///
    /// The operator republishes the status on any change to the payload, a
    /// controller's `Connected` flap included, so only a status that moved
    /// the unit into or out of `Idle` restarts the quiet window; a republish
    /// of the same activity leaves the window where it stands. The client
    /// draws every status either way.
    fn on_status(&mut self, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(status) = crate::status::parse(payload) else {
            return Vec::new();
        };
        let idle = status.activity == Activity::Idle;
        let mut effects = vec![Effect::Moment(Moment::Status(status))];
        if idle == self.idle {
            return effects;
        }
        self.idle = idle;
        let mut moment = None;
        if !self.idle && self.asleep {
            self.asleep = false;
            moment = Some(Moment::Wake);
        }
        self.rearm(now);
        self.shade(moment, &mut effects);
        effects
    }

    /// Fold one message off the volume topic. The level is held for two
    /// reasons: a volume press steps from the last level the topic delivered,
    /// and the client draws the indicator from it.
    fn on_level(&mut self, payload: &[u8], retained: bool) -> Vec<Effect> {
        let Some(volume) = crate::volume::parse(payload) else {
            return Vec::new();
        };
        self.volume = Some(volume);
        vec![Effect::Moment(Moment::Level {
            volume,
            pressed: !retained,
        })]
    }

    /// Fold one key event. A sleeping screen wakes on any press, so a person
    /// gets the screen back with whatever control they touched, and that press
    /// does nothing else. A navigation key, while the unit plays nothing,
    /// reaches the client. A level key, while the unit plays nothing and the
    /// screen is awake, publishes the unit's next level. The cycle key asks
    /// the operator to move the mark and does nothing else. Every other press
    /// restarts the quiet window.
    ///
    /// A press acts only while the remote's mark names this `Player`. A pad
    /// pointed at another room touches nothing here, not the shade and not
    /// the level. A release changes nothing at all: the standing pod holds
    /// the repeat and stops it at the release, so this crate has nothing to
    /// stop.
    fn on_press(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(press) = press::parse(payload) else {
            return Vec::new();
        };
        if !self.holds_focus(index) || !press.edge() {
            return Vec::new();
        }

        let mut moment = None;
        let mut forwarded = None;
        let mut publish = None;
        if self.idle && press.down() && press.key == keys::CYCLE {
            publish = self.cycle(index);
        } else if self.asleep {
            self.asleep = false;
            moment = Some(Moment::Wake);
        } else if self.idle
            && let Some(key) = keys::navigation(&press.key)
        {
            forwarded = Some(key);
        } else if self.idle {
            publish = self.level(&press);
        }

        self.rearm(now);
        let mut effects = Vec::new();
        self.shade(moment, &mut effects);
        if let Some(key) = forwarded {
            effects.push(Effect::Moment(Moment::Press(key)));
        }
        if let Some(publish) = publish {
            effects.push(Effect::Publish(publish));
        }
        effects
    }

    /// Fold one mark off a controller's focus topic. It sets the gate every
    /// time. A live message that names this `Player` is a person pointing the
    /// controller here: it lifts the shade, restarts the quiet window, and
    /// pulses the display with the controller's index. The session's first
    /// message is the broker's retained catch-up, so it sets the gate and
    /// does nothing else. A mark that names another `Player`, or a `Play`
    /// name left from an older operator, gates closed and pulses nothing.
    ///
    /// A mark that does not name this `Player` only closes the gate here. The
    /// standing pod synthesises the repeat and stops it at the release, so a
    /// control held as the mark moves away needs nothing stopped here.
    fn on_focus(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let mark = String::from_utf8_lossy(payload).into_owned();
        let live = self.marks[index].caught_up;
        let names_this_player = self.names_this_player(&mark);
        self.marks[index] = Mark {
            player: mark,
            caught_up: true,
        };
        if !names_this_player || !live {
            return Vec::new();
        }

        let mut moment = None;
        if self.asleep {
            self.asleep = false;
            moment = Some(Moment::Wake);
        }
        self.rearm(now);
        let mut effects = Vec::new();
        self.shade(moment, &mut effects);
        effects.push(Effect::Moment(Moment::Focus { remote: index }));
        effects
    }

    /// Fold one message off the commands topic. One command acts, the
    /// operator's re-present, and it acts only while the unit plays nothing,
    /// so a stray one during a film never maps the clock over it. Every other
    /// action does nothing.
    fn on_command(&mut self, payload: &[u8]) -> Vec<Effect> {
        let Some(command) = crate::object::<Command>(payload) else {
            return Vec::new();
        };
        if command.action != RE_PRESENT || !self.idle {
            return Vec::new();
        }
        vec![Effect::Moment(Moment::Present)]
    }

    /// The cycle request the operator arbitrates, on the controller's own
    /// cycle topic, not retained, because a cycle is an event and not a
    /// state. It is the same message the playback pod's command sidecar
    /// publishes during a film.
    fn cycle(&self, index: usize) -> Option<Publish> {
        let focus = &self.remotes[index].focus;
        if focus.is_empty() {
            return None;
        }
        Some(Publish {
            topic: focus.clone() + CYCLE_SUFFIX,
            payload: Vec::new(),
            retained: false,
        })
    }

    /// What a level press publishes, retained. A key that names no level, and
    /// a unit with no sinks, publish nothing. The level steps from the last
    /// message the topic delivered, or from unity before any message arrives.
    fn level(&self, press: &press::Press) -> Option<Publish> {
        if self.volume_topic.is_empty() {
            return None;
        }
        let next = keys::level(press, self.volume.unwrap_or_default())?;
        Some(Publish {
            topic: self.volume_topic.clone(),
            payload: next.payload(),
            retained: true,
        })
    }

    /// Add one fold's shade moment. A wake also states the on desire, which
    /// is what lifts the override. No moment is the ordinary case of a fold
    /// that changed no state, and it adds nothing.
    fn shade(&mut self, moment: Option<Moment>, effects: &mut Vec<Effect>) {
        let Some(moment) = moment else {
            return;
        };
        let wake = moment == Moment::Wake;
        effects.push(Effect::Moment(moment));
        if wake {
            self.desire(panel::ON, effects);
        }
    }

    /// Hold the new desire and publish it. An unchanged desire publishes
    /// nothing, because the broker holds the last one.
    fn desire(&mut self, desire: &'static str, effects: &mut Vec<Effect>) {
        if self.desire == desire {
            return;
        }
        self.desire = desire;
        self.publish_desire(effects);
    }

    /// The desire this client holds now, retained, so the operator reads the
    /// current one the moment it subscribes. A `Player` with no panel topic
    /// states no desire.
    fn publish_desire(&self, effects: &mut Vec<Effect>) {
        if self.panel_topic.is_empty() {
            return;
        }
        effects.push(Effect::Publish(Publish {
            topic: self.panel_topic.clone(),
            payload: panel::Desire {
                desire: self.desire,
            }
            .payload(),
            retained: true,
        }));
    }

    /// Restart the armed window from now. The quiet window runs only while
    /// the screen is awake, the unit plays nothing, and the policy is above
    /// zero; every other state leaves it disarmed. The off window runs from
    /// the moment the shade came down, so the two windows measure one quiet
    /// stretch, and it arms only while the desire is still on.
    fn rearm(&mut self, now: Instant) {
        self.deadline = None;
        if !self.idle {
            return;
        }
        if self.asleep {
            if self.off_after.is_zero() || self.desire != panel::ON {
                return;
            }
            self.deadline = Some((now + (self.off_after - self.fade_after), Window::Off));
            return;
        }
        if self.fade_after.is_zero() {
            return;
        }
        self.deadline = Some((now + self.fade_after, Window::Quiet));
    }

    /// Whether this controller's mark names this `Player` right now.
    fn holds_focus(&self, index: usize) -> bool {
        self.names_this_player(&self.marks[index].player)
    }

    /// Compare one mark against the `Player`'s own name. A client that read
    /// no name matches no mark and answers no press.
    fn names_this_player(&self, mark: &str) -> bool {
        !self.player_name.is_empty() && mark == self.player_name
    }

    /// Which controller a topic belongs to, by a scan over the unit's own
    /// controllers. An empty topic names none, so a controller the operator
    /// gave no focus topic matches nothing.
    fn remote_for(&self, topic: &str, of: impl Fn(&Remote) -> &String) -> Option<usize> {
        if topic.is_empty() {
            return None;
        }
        self.remotes.iter().position(|remote| of(remote) == topic)
    }
}

#[cfg(test)]
mod tests;
