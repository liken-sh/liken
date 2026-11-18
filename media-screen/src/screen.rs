//! The rules that a screen client follows for one `Player`: when the
//! idle screen goes dark, which remote's presses it acts on, and whether
//! it requests the monitor on or off from `media-operator`.
//!
//! A sleeping screen is one that the client has faded to black. The panel
//! request is `on` or `off`, the monitor state that the client publishes on
//! the panel topic for `media-operator` to apply.
//!
//! [`Screen`] opens no socket and reads no clock. Each method takes the
//! message and the current time and returns the effects, so a test needs
//! no broker and no thread. [`crate::Reader`] owns the connection.

mod commands;
pub mod keys;
mod power;
pub mod press;

use std::time::{Duration, Instant};

use crate::panel;
use crate::status::{Activity, Status};
use crate::volume::Volume;
use crate::wiring::{Remote, Wiring};

/// Appended to a remote's focus topic to get its cycle topic. It must
/// match `remoteFocusCycleTopic` in `media-operator`.
const CYCLE_SUFFIX: &str = "/cycle";

/// An event for the client to act on, usually by drawing something. The
/// rules in this module have already filtered it, so the client acts on
/// every one it receives.
#[derive(Debug, Clone, PartialEq)]
pub enum Moment {
    /// A key press that this crate passes to the client, with the
    /// kernel's key name, such as `KEY_UP`. The client maps keys to
    /// actions with its own table.
    Press(String),
    /// Fade the screen to black. Sent when the fade timer expires, when
    /// the client calls [`Screen::sleep`], when `media-operator` requests
    /// sleep, and when the retained panel request is `off`.
    Sleep,
    /// Show the screen again. Sent after a press, a focus change to this
    /// `Player`, a `Play` starting, or a wake request from
    /// `media-operator`. When the press that woke the screen also goes
    /// to the client, [`Moment::Press`] follows in the same result.
    Wake,
    /// A live focus message moved a remote to this `Player`, or
    /// `media-operator` responded to this client's cycle request.
    /// `remote` is the remote's index in `spec.remotes`, which is also the
    /// order of the remotes in the status.
    Focus { remote: usize },
    /// The complete `Player` status.
    Status(Status),
    /// The listening level that `media-operator` relays. `pressed` is
    /// false for the retained message that the broker sends on
    /// subscribe, and true for a live message, which means a person
    /// changed the level. For a pressed level, the client draws the
    /// volume bar only when `Volume::draws_after` returns true for the
    /// previous level.
    Level { volume: Volume, pressed: bool },
    /// A person accepted the up-next offer on the scrubber. The bytes
    /// are the `request` of the `Play`'s next item, unparsed, because
    /// the client that wrote the `Play` parses its own format. It is
    /// sent while a film plays, so it does not wait for the `Player` to
    /// be idle.
    PlayNext(Vec<u8>),
    /// A message on a topic that the client registered with
    /// [`Screen::reading`]. The rules do not read it. `retained` tells
    /// the client whether the broker sent a stored value or a live
    /// write.
    Message {
        topic: String,
        payload: Vec<u8>,
        retained: bool,
    },
    /// A bus session started, including the first one. Each program on
    /// the bus republishes its own retained state when it connects, in
    /// case the broker restarted and lost it, so a client with retained
    /// state publishes it here.
    Connected,
}

/// A message that [`crate::Reader`] publishes on its connection for the
/// rules.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Publish {
    pub topic: String,
    pub payload: Vec<u8>,
    pub retained: bool,
}

/// One result of a message or a timer: a [`Moment`] for the client, or a
/// [`Publish`] for the reader to send.
#[derive(Debug, Clone, PartialEq)]
pub enum Effect {
    Moment(Moment),
    Publish(Publish),
}

/// The last message on one remote's focus topic: the name of the
/// `Player` that the remote controls, which `media-operator` publishes
/// retained.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
struct FocusMessage {
    player: String,
    /// False until the first message of the current bus session. On
    /// subscribe the broker sends the retained message first, and that
    /// message restores state and is not a person's action, so it produces
    /// no [`Moment::Focus`]. On a focus topic with no retained message, the
    /// first live message is treated the same way and also produces none.
    received_this_session: bool,
    /// True from this client's cycle request until the next focus
    /// message. When only one `Player` lists the remote, `media-operator`
    /// responds by publishing the same `Player` again, and `on_focus`
    /// treats that repeat as a focus change so the person sees a response.
    cycle_requested: bool,
}

/// Which timer the deadline belongs to.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Timer {
    /// When it expires, the screen sleeps.
    Fade,
    /// When it expires, the client requests the panel off.
    Off,
}

/// The state of one `Player`'s screen client.
#[derive(Debug)]
pub struct Screen {
    /// A remote's presses act here only while its focus message is this
    /// name. An empty name matches no focus message.
    player_name: String,
    status_topic: String,
    /// Empty when `media-operator` does not relay this `Player`'s level.
    volume_topic: String,
    commands_topic: String,
    panel_topic: String,
    /// Where `media-operator` publishes wake and sleep requests.
    power_topic: String,
    /// In `spec.remotes` order, so an index here is the index in
    /// [`Moment::Focus`].
    remotes: Vec<Remote>,
    /// One entry per remote.
    focus: Vec<FocusMessage>,
    /// One entry per remote: the key whose repeats are ignored until its
    /// release or the remote's next press. See `on_press`.
    repeats_ignored: Vec<Option<String>>,
    /// Topics from the client's own configuration, set by
    /// [`Screen::reading`].
    client_topics: Vec<String>,

    /// The fade timer. Zero disables it.
    fade_after: Duration,
    /// The off timer, measured from the start of the fade timer.
    /// `Wiring::read` raises it to at least `fade_after`, because `rearm`
    /// subtracts `fade_after` from it. Zero disables it.
    off_after: Duration,

    /// Whether the last status had the activity `Idle`. The timers run
    /// only while it is true. It starts false, so until the retained status
    /// arrives no press goes to the client and no timer runs.
    idle: bool,
    asleep: bool,
    /// The retained panel request from the broker, or the last one this
    /// client published. `None` until one arrives. `media-operator` reads
    /// the request and overrides the screen's `Display` to match, so the
    /// client never drives the monitor itself.
    panel_request: Option<&'static str>,
    deadline: Option<(Instant, Timer)>,
    /// Log lines since the last [`Screen::take_lines`]: one for each
    /// press, each timer that expires, and each panel request published.
    /// Repeats, retained messages, and unchanged statuses write none.
    lines: Vec<String>,
}

impl Screen {
    pub fn new(wiring: &Wiring) -> Self {
        Self {
            player_name: wiring.player_name.clone(),
            status_topic: wiring.status_topic.clone(),
            volume_topic: wiring.volume_topic.clone(),
            commands_topic: wiring.commands_topic.clone(),
            panel_topic: wiring.panel_topic.clone(),
            power_topic: wiring.power_topic.clone(),
            focus: vec![FocusMessage::default(); wiring.remotes.len()],
            repeats_ignored: vec![None; wiring.remotes.len()],
            remotes: wiring.remotes.clone(),
            client_topics: Vec::new(),
            fade_after: wiring.fade_after,
            off_after: wiring.off_after,
            idle: false,
            asleep: false,
            // Wait for the retained request in `on_panel`, so a pod that
            // restarts while the panel is off keeps the panel off.
            panel_request: None,
            deadline: None,
            lines: Vec::new(),
        }
    }

    /// Returns the log lines written since the last call, oldest first.
    /// [`crate::Reader`] prints them.
    pub fn take_lines(&mut self) -> Vec<String> {
        std::mem::take(&mut self.lines)
    }

    /// Adds topics for the client's own retained state. Each message on
    /// them comes back as a [`Moment::Message`]. Empty names are dropped.
    /// They share this crate's connection, so the client does not open a
    /// second session under another client id.
    #[must_use]
    pub fn reading(mut self, client_topics: &[String]) -> Self {
        self.client_topics = client_topics
            .iter()
            .filter(|topic| !topic.is_empty())
            .cloned()
            .collect();
        self
    }

    /// Returns the topics to subscribe to, leaving out the empty ones.
    /// The focus and panel topics are retained, so the screen has each
    /// focus message and the panel request before the first press.
    pub fn filters(&self) -> Vec<String> {
        let mut filters = vec![
            self.status_topic.clone(),
            self.volume_topic.clone(),
            self.commands_topic.clone(),
            self.panel_topic.clone(),
            self.power_topic.clone(),
        ];
        for remote in &self.remotes {
            filters.push(remote.events.clone());
            filters.push(remote.focus.clone());
        }
        filters.retain(|topic| !topic.is_empty());
        filters.extend(self.client_topics.iter().cloned());
        filters
    }

    pub fn next_deadline(&self) -> Option<Instant> {
        self.deadline.map(|(at, _)| at)
    }

    /// Handles one message from any subscription and returns the
    /// effects. A message on an unknown topic, or one that does not
    /// decode, has no effect, so a newer message format does not crash
    /// an older client.
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
        if !self.volume_topic.is_empty() && topic == self.volume_topic {
            return on_level(payload, retained);
        }
        if let Some(index) = self.remote_for(topic, |remote| &remote.events) {
            return self.on_press(index, payload, now);
        }
        if let Some(index) = self.remote_for(topic, |remote| &remote.focus) {
            return self.on_focus(index, payload, now);
        }
        if !self.commands_topic.is_empty() && topic == self.commands_topic {
            return self.on_command(payload);
        }
        if !self.panel_topic.is_empty() && topic == self.panel_topic {
            return self.on_panel(payload, retained, now);
        }
        if !self.power_topic.is_empty() && topic == self.power_topic {
            return self.on_power(payload, now);
        }
        // Checked last, so a client topic that is also one of the
        // screen's topics runs only the screen's rule.
        if self.client_topics.iter().any(|owned| owned == topic) {
            return vec![Effect::Moment(Moment::Message {
                topic: topic.to_string(),
                payload: payload.to_vec(),
                retained,
            })];
        }
        Vec::new()
    }

    /// Acts on the running timer if it has expired. When the fade timer
    /// expires, the screen sleeps and the off timer starts. When the off
    /// timer expires, the client requests the panel off.
    pub fn tick(&mut self, now: Instant) -> Vec<Effect> {
        let Some((at, timer)) = self.deadline else {
            return Vec::new();
        };
        if now < at {
            return Vec::new();
        }
        let mut effects = Vec::new();
        match timer {
            Timer::Fade => {
                self.asleep = true;
                self.rearm(now);
                self.push_sleep_or_wake(Some(Moment::Sleep), &mut effects);
                self.lines.push(format!(
                    "the fade timer of {} s expired, so the screen is asleep",
                    self.fade_after.as_secs()
                ));
            }
            Timer::Off => {
                self.deadline = None;
                let panel_line = self.request_panel(panel::OFF, &mut effects);
                self.lines.push(format!(
                    "the off timer of {} s expired{panel_line}",
                    self.off_after.as_secs()
                ));
            }
        }
        effects
    }

    /// Puts the screen to sleep at the client's request, as the fade
    /// timer does, and starts the off timer. Does nothing unless the
    /// `Player` is idle and the screen is awake.
    pub fn sleep(&mut self, now: Instant) -> Vec<Effect> {
        if !self.idle || self.asleep {
            return Vec::new();
        }
        self.asleep = true;
        self.rearm(now);
        let mut effects = Vec::new();
        self.push_sleep_or_wake(Some(Moment::Sleep), &mut effects);
        self.lines
            .push("the client requested sleep, so the screen is asleep".into());
        effects
    }

    /// Handles the start of a bus session. The broker resends every
    /// retained focus message, so the next one on each topic is treated
    /// as retained again. The stored focus messages are kept, so a broker
    /// restart does not change which remote controls this `Player`.
    ///
    /// The client publishes its panel request again, which restores it on
    /// a broker that restarted. A client with no panel request publishes
    /// none, because a reconnect is no reason to change the panel.
    pub fn connected(&mut self) -> Vec<Effect> {
        for focus in &mut self.focus {
            focus.received_this_session = false;
        }
        let mut effects = Vec::new();
        self.publish_panel_request(&mut effects);
        effects.push(Effect::Moment(Moment::Connected));
        effects
    }

    /// Handles one status. The timers run only while the `Player` is
    /// idle. When it stops being idle while the screen is asleep, the
    /// screen wakes, so a `Play` started from another `Player`'s remote
    /// shows the film and not a black screen.
    ///
    /// `media-operator` republishes the status on any change, such as a
    /// remote connecting. So only a change into or out of `Idle`
    /// restarts the timer, and every other status only goes to the
    /// client.
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
        self.push_sleep_or_wake(moment, &mut effects);
        effects
    }

    /// Handles one message on the panel topic. A client with no panel
    /// request adopts the retained one, because it is the panel's current
    /// state. An adopted `off` puts the screen to sleep, so the next press
    /// wakes it and publishes `on`. A live message is this client's own
    /// publish and changes nothing.
    fn on_panel(&mut self, payload: &[u8], retained: bool, now: Instant) -> Vec<Effect> {
        if !retained || self.panel_request.is_some() {
            return Vec::new();
        }
        let Some(adopted) =
            crate::object::<panel::Received>(payload).and_then(|received| received.request())
        else {
            return Vec::new();
        };
        self.panel_request = Some(adopted);
        if adopted == panel::ON || self.asleep {
            return Vec::new();
        }
        self.asleep = true;
        self.rearm(now);
        vec![Effect::Moment(Moment::Sleep)]
    }

    /// Handles one key event from remote `index`. A remote acts only while
    /// its focus message is this `Player`.
    ///
    /// A release does not go to the client and does not wake or sleep the
    /// screen. The client needs no release, because the remote's reader pod
    /// sends the repeats while the key is down and stops them at the
    /// release. A release only clears the key from `repeats_ignored`.
    ///
    /// A power key does nothing here, because `media-operator` handles it
    /// through `spec.power` and then sends a request on the power topic.
    ///
    /// While the `Player` is idle, the cycle key publishes a cycle request
    /// and does nothing else, even on a sleeping screen. `media-operator`
    /// responds with a focus message, and `on_focus` wakes the screen.
    ///
    /// Any other press on a sleeping screen wakes it. While the `Player` is
    /// idle the press also goes to the client, so a person does not have to
    /// press twice each time the screen fades. The press only wakes the
    /// screen when the panel request is `off`, because the person could not
    /// see what it would act on, and when the key is back, for the reason
    /// that [`keys::BACK`] gives. A repeat on a sleeping screen does not
    /// wake it.
    ///
    /// When a press only wakes the screen, or a repeat arrives while it
    /// sleeps, the crate ignores that key's repeats until it is released.
    /// Without this, a held back key would put the screen to sleep again,
    /// and a held select would act on a dark panel. The remote's next press
    /// also clears the key, so a lost release does not leave it ignored.
    ///
    /// While the `Player` is idle, other presses and repeats go to the
    /// client. Every press or repeat that passes the focus, power, and
    /// `repeats_ignored` checks restarts the running timer. Only a press
    /// writes a log line, not a repeat or a release.
    fn on_press(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(press) = press::parse(payload) else {
            return Vec::new();
        };
        let trigger = format!(
            "{} from remote {}",
            press.key,
            remote_name(&self.remotes[index].events)
        );
        // Cleared even when the focus is on another `Player`, so the entry
        // does not outlive the key press.
        if !press.held() && self.repeats_ignored[index].as_deref() == Some(press.key.as_str()) {
            self.repeats_ignored[index] = None;
        }
        if !self.has_focus(index) {
            if press.down() {
                let player = &self.focus[index].player;
                self.lines.push(if player.is_empty() {
                    format!("{trigger} ignored, because the remote's focus topic names no player")
                } else {
                    format!("{trigger} ignored, because focus is on player {player}")
                });
            }
            return Vec::new();
        }
        if !press.held() {
            return Vec::new();
        }
        if keys::power(&press.key) {
            if press.down() {
                self.lines.push(format!(
                    "{trigger} ignored, because media-operator turns the player off and on"
                ));
            }
            return Vec::new();
        }
        if press.down() {
            self.repeats_ignored[index] = None;
        } else if self.repeats_ignored[index].as_deref() == Some(press.key.as_str()) {
            return Vec::new();
        } else if self.asleep {
            self.repeats_ignored[index] = Some(press.key);
            return Vec::new();
        }
        let down = press.down();

        let mut moment = None;
        let mut forwarded = None;
        let mut publish = None;
        let mut line = None;
        if self.idle && press.down() && press.key == keys::CYCLE {
            publish = self.cycle(index);
            if publish.is_some() {
                self.focus[index].cycle_requested = true;
            }
            line = Some(match &publish {
                Some(cycle) => format!(
                    "{trigger}: cycle focus, published the cycle request to {}",
                    cycle.topic
                ),
                None => format!("{trigger} ignored, because the remote has no focus topic"),
            });
        } else if self.asleep {
            // Read before the wake publishes `on`.
            let dark = self.panel_request == Some(panel::OFF);
            let back = keys::back(&press.key);
            self.asleep = false;
            moment = Some(Moment::Wake);
            if self.idle && !dark && !back {
                line = Some(format!(
                    "{trigger} woke the screen and passed to the client"
                ));
                forwarded = Some(press.key);
            } else {
                self.repeats_ignored[index] = Some(press.key.clone());
                let why = if dark {
                    ", because the panel was off"
                } else if back {
                    ", because back on a sleeping screen only wakes it"
                } else {
                    ""
                };
                line = Some(format!(
                    "{trigger} woke the screen and did nothing else{why}"
                ));
            }
        } else if self.idle && keys::cycle(&press.key) {
            // A repeat of the cycle key does nothing, so one press
            // cycles once.
        } else if self.idle {
            if press.down() {
                line = Some(format!("{trigger} passed to the client"));
            }
            forwarded = Some(press.key);
        }

        self.rearm(now);
        let mut effects = Vec::new();
        let mut panel_line = self.push_sleep_or_wake(moment, &mut effects);
        // With no panel request the client cannot tell whether the panel
        // is off. A press means a person is in the room, so publish `on`.
        if self.panel_request.is_none() && down {
            panel_line = self.request_panel(panel::ON, &mut effects);
        }
        if let Some(line) = line {
            self.lines.push(line + &panel_line);
        }
        if let Some(key) = forwarded {
            effects.push(Effect::Moment(Moment::Press(key)));
        }
        if let Some(publish) = publish {
            effects.push(Effect::Publish(publish));
        }
        effects
    }

    /// Handles one message on remote `index`'s focus topic, and always
    /// stores it.
    ///
    /// A live message that moves the remote to this `Player` means a
    /// person pointed the remote here. It wakes the screen, restarts the
    /// fade timer, and sends [`Moment::Focus`]. So does the repeated
    /// message that responds to this client's cycle request.
    ///
    /// The retained message, a message for another `Player`, and any
    /// other repeat of the stored message only update the stored one. A
    /// repeat comes from a publisher that sent the message again, such as
    /// `media-operator` after a restart, and waking on it would light the
    /// screen in an empty room.
    fn on_focus(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let player = String::from_utf8_lossy(payload).into_owned();
        let stored = &self.focus[index];
        let live = stored.received_this_session;
        let moved = stored.player != player || stored.cycle_requested;
        let names_this_player = self.names_this_player(&player);
        self.focus[index] = FocusMessage {
            player,
            received_this_session: true,
            cycle_requested: false,
        };
        if !names_this_player || !live || !moved {
            return Vec::new();
        }

        let mut moment = None;
        if self.asleep {
            self.asleep = false;
            moment = Some(Moment::Wake);
        }
        self.rearm(now);
        let mut effects = Vec::new();
        self.push_sleep_or_wake(moment, &mut effects);
        effects.push(Effect::Moment(Moment::Focus { remote: index }));
        effects
    }

    /// Builds the request for `media-operator` to move the remote's
    /// focus to the next `Player`. It is not retained, because a cycle
    /// is an event. The playback pod's command sidecar publishes the
    /// same request during a film. Returns `None` when the remote has no
    /// focus topic.
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

    /// Adds `moment` to `effects`. A wake also requests the panel `on`,
    /// which removes the override on the screen's `Display`.
    ///
    /// Returns the end of a log line about the panel request, or an empty
    /// string when the request did not change.
    fn push_sleep_or_wake(&mut self, moment: Option<Moment>, effects: &mut Vec<Effect>) -> String {
        let Some(moment) = moment else {
            return String::new();
        };
        let wake = moment == Moment::Wake;
        effects.push(Effect::Moment(moment));
        if wake {
            return self.request_panel(panel::ON, effects);
        }
        String::new()
    }

    /// Stores and publishes `request`, unless it equals the stored one,
    /// which the broker already retains.
    ///
    /// Returns the end of a log line about the request, or an empty
    /// string when it did not change.
    fn request_panel(&mut self, request: &'static str, effects: &mut Vec<Effect>) -> String {
        if self.panel_request == Some(request) {
            return String::new();
        }
        self.panel_request = Some(request);
        self.publish_panel_request(effects);
        if self.panel_topic.is_empty() {
            return format!(", and the player has no panel topic to request {request}");
        }
        format!(
            ", published panel request {request} to {}",
            self.panel_topic
        )
    }

    /// Publishes the stored panel request retained, so `media-operator`
    /// receives it as soon as it subscribes.
    fn publish_panel_request(&self, effects: &mut Vec<Effect>) {
        let Some(request) = self.panel_request else {
            return;
        };
        if self.panel_topic.is_empty() {
            return;
        }
        effects.push(Effect::Publish(Publish {
            topic: self.panel_topic.clone(),
            payload: panel::Request { desire: request }.payload(),
            retained: true,
        }));
    }

    /// Restarts the timer that fits the current state, or stops both.
    /// While idle and awake, the fade timer runs for `fade_after`. While
    /// idle and asleep with the panel request not `off`, the off timer
    /// runs for `off_after - fade_after`, so it expires `off_after` after
    /// the fade timer started. A zero duration disables its timer.
    fn rearm(&mut self, now: Instant) {
        self.deadline = None;
        if !self.idle {
            return;
        }
        if self.asleep {
            if self.off_after.is_zero() || self.panel_request == Some(panel::OFF) {
                return;
            }
            self.deadline = Some((now + (self.off_after - self.fade_after), Timer::Off));
            return;
        }
        if self.fade_after.is_zero() {
            return;
        }
        self.deadline = Some((now + self.fade_after, Timer::Fade));
    }

    fn has_focus(&self, index: usize) -> bool {
        self.names_this_player(&self.focus[index].player)
    }

    fn names_this_player(&self, player: &str) -> bool {
        !self.player_name.is_empty() && player == self.player_name
    }

    /// Returns the index of the remote whose topic, chosen by `of`, is
    /// `topic`. An empty topic matches no remote, so a remote with no
    /// focus topic never matches.
    fn remote_for(&self, topic: &str, of: impl Fn(&Remote) -> &String) -> Option<usize> {
        if topic.is_empty() {
            return None;
        }
        self.remotes.iter().position(|remote| of(remote) == topic)
    }
}

fn on_level(payload: &[u8], retained: bool) -> Vec<Effect> {
    let Some(volume) = crate::volume::parse(payload) else {
        return Vec::new();
    };
    vec![Effect::Moment(Moment::Level {
        volume,
        pressed: !retained,
    })]
}

/// Returns `namespace/name` of the `Remote` that a topic of the form
/// `<base>/remotes/<namespace>/<name>/<kind>` belongs to. The base can
/// contain slashes, so the search starts from the last `remotes`
/// segment. Any other topic is returned unchanged, so a log line always
/// names its source.
fn remote_name(topic: &str) -> String {
    let parts: Vec<&str> = topic.split('/').collect();
    parts
        .iter()
        .rposition(|part| *part == "remotes")
        .filter(|index| index + 2 < parts.len())
        .map_or_else(
            || topic.to_string(),
            |index| format!("{}/{}", parts[index + 1], parts[index + 2]),
        )
}

#[cfg(test)]
mod tests;
