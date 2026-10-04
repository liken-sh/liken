//! The rules a screen client holds for one `Player`: the quiet window and the
//! off window, the focus gate, the shade, the level the operator relays,
//! the cycle request, and the panel desire.
//!
//! [`Screen`] reaches no socket and holds no clock. Every rule below is
//! a function of what arrived and what time it is, so a test proves
//! each one with no broker and no thread, and [`crate::Reader`] is the
//! only part of the crate that opens anything.

mod commands;
pub mod keys;
mod power;
pub mod press;

use std::time::{Duration, Instant};

use crate::panel;
use crate::status::{Activity, Power, Status};
use crate::volume::Volume;
use crate::wiring::{Remote, Wiring};

/// The suffix that turns a remote's focus topic into its cycle topic, the
/// same path `media-operator`'s `remoteFocusCycleTopic` builds, so a client
/// needs no second topic list.
const CYCLE_SUFFIX: &str = "/cycle";

/// The action a power press on a unit whose screen is wired through a
/// Receiver publishes on the power topic for a toggle. The equipment
/// operator answers it by flipping the room's power and selecting the
/// input.
const POWER_TOGGLE: &str = "toggle";

/// One thing the client draws.
///
/// Each one is a fact the screen shows or a moment it moves on, and
/// none of them is a decision the client makes again. The shade moments
/// say which way the cover eases. A focus names the controller a live
/// mark landed on, by its place in `spec.remotes`.
#[derive(Debug, Clone, PartialEq)]
pub enum Moment {
    /// One press this crate does not act on itself, under the kernel's
    /// name for the control. The client holds its own table from these
    /// names to what they do there, so a letter key on a remote with a
    /// keyboard reaches a client that types.
    Press(String),
    /// The quiet window ran out, or the client asked for the shade.
    Sleep,
    /// A press, a live mark, or a starting `Play` lifted the shade.
    Wake,
    /// A live mark named this `Player`. `remote` is the controller's place in
    /// `spec.remotes`, which is the order the status lists the parts in.
    Focus { remote: usize },
    /// The unit's whole presentable state.
    Status(Status),
    /// The unit's listening level, as `media-operator` relays it. `pressed` is
    /// false for the broker's catch-up and true for a live message, which is
    /// a person changing the level with a remote or at the device. A client
    /// draws the bar for a pressed level only when `Volume::draws_after` the
    /// level it held says so.
    Level { volume: Volume, pressed: bool },
    /// A person took the up-next offer on the scrubber. The bytes are the
    /// `request` of the `Play`'s next block, which this crate never reads:
    /// the client that wrote the `Play` reads its own words back and starts
    /// what follows. The moment fires whether or not the unit is idle,
    /// because the unit is never idle when this arrives.
    PlayNext(Vec<u8>),
    /// One message on a topic the client owns. The rules read nothing in
    /// the payload and hold no state from it, because the topic is the
    /// client's own. `retained` is the broker's mark on the delivery, so a
    /// client tells the catch-up on a topic it owns from a live write.
    Message {
        topic: String,
        payload: Vec<u8>,
        retained: bool,
    },
    /// The bus session started. A client that owns retained state
    /// republishes it here, which is the bus rule that each connected
    /// program republishes the retained state it owns when its session
    /// reconnects. The moment arrives on the first session too, because a
    /// client cannot tell that one from a reconnect.
    Connected,
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
#[derive(Debug, Clone, PartialEq)]
pub enum Effect {
    Moment(Moment),
    Publish(Publish),
}

/// One controller's mark and whether this bus session already delivered one.
/// The first message of a session is the broker's retained catch-up, a
/// restore and not a person, so it sets the gate and pulses nothing.
///
/// `cycle_asked` is set while this client's cycle request on the controller
/// waits for its answer. On a controller that one unit lists, the operator
/// answers with the same mark, and that repeat is the press's feedback.
/// Every other repeat of the mark is a publisher that sent it again, and a
/// person did nothing.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
struct Mark {
    player: String,
    caught_up: bool,
    cycle_asked: bool,
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
    /// The unit's volume topic, empty for a `Player` whose level the operator
    /// does not relay. Empty is the speaker gate: the client subscribes to no
    /// level.
    volume_topic: String,
    commands_topic: String,
    panel_topic: String,
    /// The topic a power press publishes its ask on in the room mode. A
    /// current operator sets it for every unit, and an operator that
    /// predates the power mode sets it only for a unit with a `Receiver`.
    power_topic: String,
    /// The power mode the last status that stated one named, or `None`
    /// while no status stated one. [`Screen::room_power`] reads it at each
    /// press, so a `Receiver` that is wired or removed while the client runs
    /// moves the next press.
    power: Option<Power>,
    /// The unit's controllers, in `spec.remotes` order, so a controller's
    /// index in this list is the index a focus moment carries.
    remotes: Vec<Remote>,
    /// The last mark each controller's focus topic delivered, one per entry
    /// of `remotes`.
    marks: Vec<Mark>,
    /// The topics the client owns. They are the client's own configuration
    /// and not the `Player`'s wiring, so they arrive from the client and not
    /// from a variable the operator sets.
    client_topics: Vec<String>,

    /// The quiet window. Zero never arms the timer.
    fade_after: Duration,
    /// The off window, clamped to at least the fade. Zero leaves the desire
    /// at on forever.
    off_after: Duration,

    /// Whether the last status named the activity `Idle`, the only state the
    /// timer arms in. It starts false, so a client answers no press until the
    /// retained status reaches it.
    idle: bool,
    /// Whether the shade is down.
    asleep: bool,
    /// The panel desire as the client knows it: the retained desire the
    /// broker delivered, or the one this client last stated. `None` is a
    /// desire not read yet, and the client states none until a press or a
    /// window changes the panel. The client writes no hardware; the
    /// operator reads the desire from the bus and overrides the screen's
    /// `Display`.
    desire: Option<&'static str>,
    /// The armed window and the moment it runs out.
    deadline: Option<(Instant, Window)>,
    /// The moment a power ask that waits for `Idle` is dropped, and the key
    /// the ask is answered as, or `None` while no ask waits. [`commands`]
    /// holds the rule.
    power_ask: Option<(Instant, &'static str)>,
    /// The lines the folds since the last [`Screen::take_lines`] wrote, one
    /// per operation a person caused: a press and what it did or why it did
    /// nothing, a window that brought the shade down, a panel desire. A
    /// repeat, a catch-up, and a status that moves nothing write none.
    lines: Vec<String>,
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
            power_topic: wiring.power_topic.clone(),
            power: None,
            marks: vec![Mark::default(); wiring.remotes.len()],
            remotes: wiring.remotes.clone(),
            client_topics: Vec::new(),
            fade_after: wiring.fade_after,
            off_after: wiring.off_after,
            idle: false,
            asleep: false,
            // A client that starts has not read the retained desire yet. It
            // adopts the one the broker holds, so a pod that restarts in a
            // dark room keeps the room dark.
            desire: None,
            deadline: None,
            power_ask: None,
            lines: Vec::new(),
        }
    }

    /// The lines the folds wrote since the last call, oldest first.
    /// [`crate::Reader`] prints them, so a client that holds a reader logs
    /// them with no code of its own.
    pub fn take_lines(&mut self) -> Vec<String> {
        std::mem::take(&mut self.lines)
    }

    /// The same screen, plus the topics the client owns. A client that keeps
    /// retained state of its own, such as a mark for the person watching,
    /// names those topics here and reads every message on them back as a
    /// [`Moment::Message`]. An empty name is no topic and is dropped.
    ///
    /// The rules read nothing on these topics. They are a second
    /// subscription on the one connection this crate holds, so a client
    /// opens no session of its own under a second identifier.
    #[must_use]
    pub fn reading(mut self, client_topics: &[String]) -> Self {
        self.client_topics = client_topics
            .iter()
            .filter(|topic| !topic.is_empty())
            .cloned()
            .collect();
        self
    }

    /// The topics to subscribe to. An empty topic is one the operator did not
    /// set, and a unit whose level the operator does not relay has no volume
    /// topic at all.
    ///
    /// A press on any of the unit's controllers reaches the quiet window, so
    /// every events topic is read. The focus topic is retained, so each mark
    /// arrives on subscribe and the gate stands before the first press. The
    /// panel topic is read for the same reason: the retained desire arrives
    /// before the client states one.
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

    /// The earlier of the moment the armed window runs out and the moment a
    /// held power ask is dropped, and nothing while neither is armed.
    pub fn next_deadline(&self) -> Option<Instant> {
        let window = self.deadline.map(|(at, _)| at);
        match (window, self.power_ask.map(|(at, _)| at)) {
            (Some(window), Some(ask)) => Some(window.min(ask)),
            (window, ask) => window.or(ask),
        }
    }

    /// Fold one message from any subscription, and the topic says which
    /// message this is. A topic this screen did not subscribe to, and a
    /// payload that does not decode, are both nothing at all, so a newer
    /// message on a topic has no effect rather than a crash.
    ///
    /// `retained` is the broker's own mark on a delivery from its retained
    /// store. A retained level is the catch-up, which no person changed, so
    /// it sets the level and shows no indicator; a live level is a change.
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
            return on_level(payload, retained);
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
            return self.on_command(payload, now);
        }
        if !self.panel_topic.is_empty() && topic == self.panel_topic {
            return self.on_panel(payload, retained, now);
        }
        if !self.power_topic.is_empty() && topic == self.power_topic {
            return self.on_power(payload, now);
        }
        // The client's own topics are read last, so a topic that is also one
        // of the screen's fires the screen's rule alone and never twice.
        if self.client_topics.iter().any(|owned| owned == topic) {
            return vec![Effect::Moment(Moment::Message {
                topic: topic.to_string(),
                payload: payload.to_vec(),
                retained,
            })];
        }
        Vec::new()
    }

    /// The armed window running out: the quiet window brings the shade down
    /// and starts the off window, and the off window states the off desire
    /// and arms nothing. A held power ask whose deadline passed is dropped
    /// first.
    pub fn tick(&mut self, now: Instant) -> Vec<Effect> {
        self.expire_power_ask(now);
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
                self.lines.push(format!(
                    "the quiet window of {} s ran out, so the shade is down",
                    self.fade_after.as_secs()
                ));
            }
            Window::Off => {
                self.deadline = None;
                let desire = self.desire(panel::OFF, &mut effects);
                self.lines.push(format!(
                    "the off window of {} s ran out{desire}",
                    self.off_after.as_secs()
                ));
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
        self.lines
            .push("the client asked for the shade, so the shade is down".into());
        effects
    }

    /// The start of every bus session. A fresh session redelivers every
    /// retained mark, so each one is a catch-up again and pulses nothing. The
    /// mark itself stands across the reconnect, so the gate does not open or
    /// close on a broker restart alone.
    ///
    /// The panel desire is this client's own retained state, so a desire the
    /// client holds goes out again on every session, and a broker that
    /// restarted holds it again. A client that has read no desire and stated
    /// none sends nothing, because a restart is not a reason to change the
    /// panel.
    ///
    /// [`Moment::Connected`] tells the client the same thing, so a client
    /// republishes the retained state it owns on the topics it named.
    pub fn connected(&mut self) -> Vec<Effect> {
        for mark in &mut self.marks {
            mark.caught_up = false;
        }
        let mut effects = Vec::new();
        self.publish_desire(&mut effects);
        effects.push(Effect::Moment(Moment::Connected));
        effects
    }

    /// Fold one status. `Idle` is the only activity the timer arms in, so a
    /// status that leaves `Idle` disarms it. The same status lifts the shade
    /// if the screen sleeps, so a `Play` started from another room shows its
    /// film and not a black screen. A status that moves the unit into `Idle`
    /// answers a held power ask, after the client draws the status.
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
        // The mode is read before the activity is compared, because the
        // operator republishes the status when only the mode moves, and a
        // held power ask below is answered in the mode this status states.
        if let Some(power) = status.power {
            self.power = Some(power);
        }
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
        if self.idle
            && let Some((_, key)) = self.power_ask.take()
        {
            self.answer_power_ask(key, &mut effects);
        }
        effects
    }

    /// Fold one message off the panel topic. The retained desire is what the
    /// panel shows now, so a client that holds no desire yet adopts it. An
    /// adopted off desire is a dark panel, so the shade comes down with it:
    /// a press then wakes the screen and states the on desire. A live message
    /// is this client's own publish coming back, and it changes nothing.
    fn on_panel(&mut self, payload: &[u8], retained: bool, now: Instant) -> Vec<Effect> {
        if !retained || self.desire.is_some() {
            return Vec::new();
        }
        let Some(adopted) =
            crate::object::<panel::Stated>(payload).and_then(|stated| stated.desire())
        else {
            return Vec::new();
        };
        self.desire = Some(adopted);
        if adopted == panel::ON || self.asleep {
            return Vec::new();
        }
        self.asleep = true;
        self.rearm(now);
        vec![Effect::Moment(Moment::Sleep)]
    }

    /// Fold one key event. The checks run in this order. The cycle key
    /// asks the operator to move the mark and does nothing else. A power
    /// key, in the room mode, reaches the equipment and never the client:
    /// it publishes the toggle and nothing else, and the shade and the
    /// panel desire stand as they were. A sleeping screen wakes on any
    /// other press, so a person gets the screen back with whatever control
    /// they touched, and that press does nothing else. Every other key, while the unit plays
    /// nothing, reaches the client. Every press restarts the quiet window.
    ///
    /// A press acts only while the remote's mark names this `Player`. A pad
    /// pointed at another room touches nothing here, not the shade and not
    /// the client. A release changes nothing at all: the standing pod holds
    /// the repeat and stops it at the release, so this crate has nothing to
    /// stop.
    fn on_press(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let Some(press) = press::parse(payload) else {
            return Vec::new();
        };
        // A press is one line, and only the press: a repeat and a release
        // are the same act of a person, and the line already says what the
        // press did.
        let trigger = format!(
            "{} from remote {}",
            press.key,
            remote_name(&self.remotes[index].events)
        );
        if !self.holds_focus(index) {
            if press.down() {
                let mark = &self.marks[index].player;
                self.lines.push(if mark.is_empty() {
                    format!(
                        "{trigger} ignored, because no focus mark names a player for this remote"
                    )
                } else {
                    format!("{trigger} ignored, because focus is on player {mark}")
                });
            }
            return Vec::new();
        }
        if !press.edge() {
            return Vec::new();
        }
        let down = press.down();

        let mut moment = None;
        let mut forwarded = None;
        let mut publish = None;
        let mut line = None;
        let mut power = false;
        if self.idle && press.down() && press.key == keys::CYCLE {
            publish = self.cycle(index);
            if publish.is_some() {
                self.marks[index].cycle_asked = true;
            }
            line = Some(match &publish {
                Some(cycle) => format!(
                    "{trigger}: cycle focus, published the cycle request to {}",
                    cycle.topic
                ),
                None => format!("{trigger} ignored, because the remote has no focus topic"),
            });
        } else if let Some(action) =
            keys::power_action(&press.key).filter(|_| self.idle && self.room_power())
        {
            // A room with a receiver answers the power key itself, so the
            // key never reaches the client and the shade never operates:
            // power turns the equipment, and nothing else. Only the
            // equipment operator knows whether the press turns the room off
            // or on, because it reads the TV's power. A wake here would
            // state the on desire, the session would turn awake, and the
            // equipment operator would wake the TV and the receiver and
            // cancel the standby the same press asked for. So the press
            // leaves the shade and the desire as they were. A press that
            // turns the room on wakes the TV through the equipment
            // operator, and the next press wakes this screen the way any
            // press does. A held key that repeated would flip the equipment
            // on and off under the hand, so only the press publishes. In the
            // screen mode the press falls through to the ordinary rules
            // below, and the client lowers its shade.
            //
            // The two deterministic power functions of a TV remote publish
            // off and on in place of the toggle (keys::POWER_OFF), and the
            // equipment operator leaves a room that is already off or on
            // as it is.
            power = true;
            if press.down() {
                publish = Some(self.power_publish(action));
                line = Some(if action == POWER_TOGGLE {
                    format!(
                        "{trigger}: power, published the toggle to {}",
                        self.power_topic
                    )
                } else {
                    format!(
                        "{trigger}: power {action}, published {action} to {}",
                        self.power_topic
                    )
                });
            }
        } else if self.asleep && press.key == keys::POWER_OFF {
            // A Power Off Function keeps a device in standby when repeated
            // (HDMI-CEC 1.3a, CEC 13.13.3), so on a unit with no Receiver it
            // leaves a sleeping screen asleep, where every other key wakes
            // it.
            power = true;
            if press.down() {
                line = Some(format!(
                    "{trigger} ignored, because the screen is already asleep"
                ));
            }
        } else if self.asleep {
            self.asleep = false;
            moment = Some(Moment::Wake);
            line = Some(format!("{trigger} woke the screen and did nothing else"));
        } else if self.idle && keys::owned(&press.key) {
            // A repeat of the cycle key asks nothing: one press is one
            // cycle, and the key is the crate's, so it never reaches the
            // client.
        } else if self.idle {
            if press.down() {
                line = Some(format!("{trigger} passed to the client"));
            }
            forwarded = Some(press.key);
        }

        self.rearm(now);
        let mut effects = Vec::new();
        let mut desire = self.shade(moment, &mut effects);
        // A client that read no desire does not know whether the panel is
        // dark. A press is a person in the room, so it states the on desire.
        // A power press states none, for the reason its branch gives.
        if self.desire.is_none() && down && !power {
            desire = self.desire(panel::ON, &mut effects);
        }
        if let Some(line) = line {
            self.lines.push(line + &desire);
        }
        if let Some(key) = forwarded {
            effects.push(Effect::Moment(Moment::Press(key)));
        }
        if let Some(publish) = publish {
            effects.push(Effect::Publish(publish));
        }
        effects
    }

    /// Fold one mark off a controller's focus topic. It sets the gate every
    /// time. A live message that moves the mark to this `Player` is a person
    /// pointing the controller here: it lifts the shade, restarts the quiet
    /// window, and pulses the display with the controller's index. So does
    /// the repeat that answers this client's own cycle request. The
    /// session's first message is the broker's retained catch-up, so it sets
    /// the gate and does nothing else. A mark that names another `Player`, or
    /// a `Play` name left from an older operator, gates closed and pulses
    /// nothing.
    ///
    /// Any other repeat of the mark this client already holds is a publisher
    /// that sent the same mark again, such as an operator after a restart,
    /// and not a person. It changes nothing, because a wake here would light
    /// a sleeping screen in a dark room.
    ///
    /// A mark that does not name this `Player` only closes the gate here. The
    /// standing pod synthesises the repeat and stops it at the release, so a
    /// control held as the mark moves away needs nothing stopped here.
    fn on_focus(&mut self, index: usize, payload: &[u8], now: Instant) -> Vec<Effect> {
        let mark = String::from_utf8_lossy(payload).into_owned();
        let held = &self.marks[index];
        let live = held.caught_up;
        let moved = held.player != mark || held.cycle_asked;
        let names_this_player = self.names_this_player(&mark);
        self.marks[index] = Mark {
            player: mark,
            caught_up: true,
            cycle_asked: false,
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
        self.shade(moment, &mut effects);
        effects.push(Effect::Moment(Moment::Focus { remote: index }));
        effects
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

    /// Whether a power press is an ask for the room, read at the press and at
    /// the answer to a held power ask. The room mode needs the power topic to
    /// publish on. A client that read no mode follows the rule of an operator
    /// that predates the field, which set the topic only for a unit with a
    /// `Receiver`.
    fn room_power(&self) -> bool {
        !self.power_topic.is_empty() && self.power != Some(Power::Screen)
    }

    /// The ask a power press publishes on a unit whose screen is wired
    /// through a Receiver: toggle, on, or off, not retained, because an ask
    /// is an event and not a state. A power ask the playback pod held until
    /// `Idle` publishes the same ask.
    fn power_publish(&self, action: &str) -> Publish {
        Publish {
            topic: self.power_topic.clone(),
            payload: format!(r#"{{"action":"{action}"}}"#).into_bytes(),
            retained: false,
        }
    }

    /// Add one fold's shade moment. A wake also states the on desire, which
    /// is what lifts the override. No moment is the ordinary case of a fold
    /// that changed no state, and it adds nothing.
    ///
    /// The answer is the end of a line that says the desire went out, or
    /// nothing when no desire moved.
    fn shade(&mut self, moment: Option<Moment>, effects: &mut Vec<Effect>) -> String {
        let Some(moment) = moment else {
            return String::new();
        };
        let wake = moment == Moment::Wake;
        effects.push(Effect::Moment(moment));
        if wake {
            return self.desire(panel::ON, effects);
        }
        String::new()
    }

    /// Hold the new desire and publish it. An unchanged desire publishes
    /// nothing, because the broker holds the last one. A client that holds
    /// no desire yet publishes any desire, because it has no value to match.
    ///
    /// The answer is the end of a line that says where the desire went, or
    /// nothing when it did not move.
    fn desire(&mut self, desire: &'static str, effects: &mut Vec<Effect>) -> String {
        if self.desire == Some(desire) {
            return String::new();
        }
        self.desire = Some(desire);
        self.publish_desire(effects);
        if self.panel_topic.is_empty() {
            return format!(", and the player has no panel topic for the {desire} desire");
        }
        format!(", published panel desire {desire} to {}", self.panel_topic)
    }

    /// The desire this client holds now, retained, so the operator reads the
    /// current one the moment it subscribes. A `Player` with no panel topic
    /// states no desire.
    fn publish_desire(&self, effects: &mut Vec<Effect>) {
        let Some(desire) = self.desire else {
            return;
        };
        if self.panel_topic.is_empty() {
            return;
        }
        effects.push(Effect::Publish(Publish {
            topic: self.panel_topic.clone(),
            payload: panel::Desire { desire }.payload(),
            retained: true,
        }));
    }

    /// Restart the armed window from now. The quiet window runs only while
    /// the screen is awake, the unit plays nothing, and the policy is above
    /// zero; every other state leaves it disarmed. The off window runs from
    /// the moment the shade came down, so the two windows measure one quiet
    /// stretch, and it arms only while the panel is not already dark.
    fn rearm(&mut self, now: Instant) {
        self.deadline = None;
        if !self.idle {
            return;
        }
        if self.asleep {
            if self.off_after.is_zero() || self.desire == Some(panel::OFF) {
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

/// Fold one message off the volume topic into the moment the client draws.
/// The screen holds no level of its own, because no rule here reads it.
fn on_level(payload: &[u8], retained: bool) -> Vec<Effect> {
    let Some(volume) = crate::volume::parse(payload) else {
        return Vec::new();
    };
    vec![Effect::Moment(Moment::Level {
        volume,
        pressed: !retained,
    })]
}

/// The `Remote` a controller topic belongs to, as `namespace/name`. Every
/// controller topic is `<base>/remotes/<namespace>/<name>/<kind>`, and the
/// base can hold slashes of its own, so the name is found from the remotes
/// segment and not from the start. A topic of another shape names itself,
/// so a line never loses its trigger.
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
