//! The preview keys, the workstation stand-in for the bus, and the legend that
//! names them on the screen itself.
//!
//! On a cluster the idle command pod publishes the `Player`'s status, its level,
//! and the screen's moments, and the pod has no keyboard. On a workstation
//! `local/idle` sets `IDLE_PREVIEW=1` and the keys below build the same
//! [`Moment`] values those topics carry. The client folds each one through
//! `Client::receive`, the call the bus reader's own messages take, so a person
//! plays every ramp, every dim, and every beat against the real handlers and
//! not against a second set written for a workstation.

use iced_winit::core::Point;

use super::{Frame, Layout};
use crate::look;
use media_screen::Moment;
use media_screen::status::{Activity, Component, Play, Status};
use media_screen::volume::{MIN_LEVEL, UNITY_LEVEL, Volume};

/// The kind the preview gives every part but the last. A screen and a set of
/// speakers both draw at full brightness with no presence, so one word covers
/// both and the last part alone carries the presence the `d` key toggles.
const SINK: &str = "sink";

/// The `Play` the preview names while one starts or runs. The operator
/// publishes the object's name and the one line the screen draws, so the
/// preview states both.
const PLAY_NAME: &str = "sailing";
const PLAY_TITLE: &str = "Sailing";

/// The controller a focus moment names. A focus counts the controllers in the
/// order the status lists them, and the preview's status lists one, so the
/// index is always the first.
const REMOTE: usize = 0;

/// How far one volume key moves the level, out of the 100 that is unity: a
/// step big enough to read on the bar at a glance, and small enough that the
/// range takes twenty of them.
const VOLUME_STEP: i64 = 5;

/// The state behind the keys: what the fake status says about the unit now.
///
/// The bus holds this state on a cluster, and the client only reads it. With no
/// broker the presses have to hold it themselves, so each key changes one field
/// here and then states the whole unit again, the way the operator republishes
/// a whole status for one change.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Keys {
    /// The unit's name and its parts, from the same environment seed the
    /// operator sets on the idle container. The preview calls the last part the
    /// remote, because the parts read in the order the operator lists them and
    /// a controller is the part a person adds last.
    name: String,
    parts: Vec<String>,
    activity: Activity,
    /// Whether the remote is connected. The `d` key toggles it.
    connected: bool,
    /// Whether the status marks the remote focused. The `f` key sets it and
    /// beats the marker, the way a live mark landing here does, and the `g` key
    /// clears it, the way a cycle onto another unit does.
    focused: bool,
    /// Whether the `s` key last put the screen to sleep, so the one key plays
    /// the two edges of the fade in turn.
    asleep: bool,
    volume: Volume,
}

impl Keys {
    /// The keys for a unit the environment named. The seed carries names and no
    /// kinds, so every part reads as a sink until the last one, which is the
    /// remote.
    pub fn seeded(name: String, parts: Vec<String>) -> Self {
        Self {
            name,
            parts,
            activity: Activity::Idle,
            connected: true,
            focused: false,
            asleep: false,
            volume: Volume::default(),
        }
    }

    /// The messages one key press stands for, in the order the bus would carry
    /// them. A key this module does not bind carries nothing, so the arrow keys
    /// the harness also delivers change nothing here.
    pub fn press(&mut self, key: &str) -> Vec<Moment> {
        match key {
            "p" => vec![self.doing(Activity::Starting)],
            "o" => vec![self.doing(Activity::Playing)],
            // The end of a film: the status returns to `Idle` and the sidecar
            // reports the idle surface back in view, in that order.
            "i" => vec![self.doing(Activity::Idle), Moment::Present],
            "d" => {
                self.connected = !self.connected;
                vec![self.status()]
            }
            // A live focus message naming this unit: the status marks the
            // remote and the moment beats its marker. A first press draws the
            // marker and beats it, and every later press beats it again, which
            // is the cycle press that wraps onto the unit already focused.
            "f" => {
                self.focused = true;
                vec![self.status(), Moment::Focus { remote: REMOTE }]
            }
            // The focus cycling away. The status stops marking the remote, the
            // marker goes, and nothing beats.
            "g" => {
                self.focused = false;
                vec![self.status()]
            }
            "s" => {
                self.asleep = !self.asleep;
                vec![match self.asleep {
                    true => Moment::Sleep,
                    false => Moment::Wake,
                }]
            }
            "9" => vec![self.step(-VOLUME_STEP)],
            "0" => vec![self.step(VOLUME_STEP)],
            "m" => {
                self.volume.muted = !self.volume.muted;
                vec![self.level()]
            }
            _ => Vec::new(),
        }
    }

    /// Move the unit to one activity and state the whole status again.
    fn doing(&mut self, activity: Activity) -> Moment {
        self.activity = activity;
        self.status()
    }

    /// The status as the operator would publish it now. The `Play` block
    /// appears only while a `Play` starts or runs, which is the rule the
    /// operator writes it under.
    fn status(&self) -> Moment {
        let last = self.parts.len().saturating_sub(1);
        let components = self
            .parts
            .iter()
            .enumerate()
            .map(|(index, name)| {
                if index == last {
                    Component {
                        name: name.clone(),
                        kind: Component::REMOTE.to_string(),
                        connected: Some(self.connected),
                        focused: self.focused.then_some(true),
                    }
                } else {
                    Component {
                        name: name.clone(),
                        kind: SINK.to_string(),
                        connected: None,
                        focused: None,
                    }
                }
            })
            .collect();

        Moment::Status(Status {
            display_name: self.name.clone(),
            activity: self.activity,
            play: (self.activity != Activity::Idle).then(|| Play {
                name: PLAY_NAME.to_string(),
                title: PLAY_TITLE.to_string(),
            }),
            components,
        })
    }

    /// Move the level by one step, held inside the range the operator holds it
    /// in, and state it.
    fn step(&mut self, step: i64) -> Moment {
        self.volume.level = (self.volume.level + step).clamp(MIN_LEVEL, UNITY_LEVEL);
        self.level()
    }

    /// The level as a press. The first level of a bus session is the broker's
    /// retained catch-up, which a person did not press and which draws no
    /// indicator. A key is a press every time, so the indicator shows every
    /// time.
    fn level(&self) -> Moment {
        Moment::Level {
            volume: self.volume,
            pressed: true,
        }
    }
}

/// The legend, one key and what it does per pair, in the order the line reads
/// them. The screen names the keys itself and not only in this file.
const LEGEND: [(&str, &str); 10] = [
    ("p", "play starts"),
    ("o", "playing"),
    ("i", "film ends"),
    ("d", "presence"),
    ("f", "focus"),
    ("g", "unfocus"),
    ("s", "sleep"),
    ("9/0", "volume"),
    ("m", "mute"),
    // The one key of the legend this module does not bind. The harness reads
    // the quit key itself, and the legend names it beside the rest because a
    // person reading the line needs the way out.
    (crate::harness::QUIT, "quit"),
];

/// The gap between one pair and the next, wide enough that a reader takes the
/// key and its words as one.
const LEGEND_GAP: &str = "    ";

/// The legend as one line.
fn legend_line() -> String {
    LEGEND
        .iter()
        .map(|(key, does)| format!("{key} {does}"))
        .collect::<Vec<String>>()
        .join(LEGEND_GAP)
}

/// Draw the legend. The client calls this only where the preview keys are
/// bound, so a cluster's idle screen carries none.
///
/// The line hangs from its own bottom-right corner, which mirrors the identity
/// block's bottom-left across the screen.
pub fn legend(frame: &mut Frame, layout: &Layout, light: f32) {
    frame.fill_text(look::line(
        legend_line(),
        Point::new(layout.right(), layout.bottom()),
        look::Anchor::BottomRight,
        look::TINY,
        look::under(look::faded(look::muted(), look::DIM), light),
    ));
}

#[cfg(test)]
mod tests {
    use super::*;

    fn keys() -> Keys {
        Keys::seeded(
            "Studio Lab".to_string(),
            vec![
                "Portable Screen".to_string(),
                "Built-in Speakers".to_string(),
                "Studio Dualsense".to_string(),
            ],
        )
    }

    fn status(moment: &Moment) -> &Status {
        let Moment::Status(status) = moment else {
            panic!("the press states a status");
        };
        status
    }

    #[test]
    fn the_seed_names_the_unit_and_its_parts() {
        let pressed = keys().press("d");
        let status = status(&pressed[0]);

        assert_eq!(status.display_name, "Studio Lab");
        assert_eq!(
            status
                .components
                .iter()
                .map(|part| part.name.as_str())
                .collect::<Vec<_>>(),
            ["Portable Screen", "Built-in Speakers", "Studio Dualsense"]
        );
    }

    #[test]
    fn the_last_part_is_the_remote_and_every_other_part_is_a_sink() {
        let pressed = keys().press("d");
        let parts = &status(&pressed[0]).components;

        assert_eq!(parts[0].kind, SINK);
        assert_eq!(parts[0].connected, None);
        assert_eq!(parts[2].kind, Component::REMOTE);
        assert_eq!(parts[2].connected, Some(false));
    }

    #[test]
    fn a_unit_with_no_parts_states_none() {
        let mut keys = Keys::seeded("Studio Lab".to_string(), Vec::new());
        assert!(status(&keys.press("d")[0]).components.is_empty());
    }

    #[test]
    fn the_play_keys_state_the_three_activities() {
        let mut keys = keys();

        assert_eq!(status(&keys.press("p")[0]).activity, Activity::Starting);
        assert_eq!(status(&keys.press("o")[0]).activity, Activity::Playing);
        assert_eq!(status(&keys.press("i")[0]).activity, Activity::Idle);
    }

    #[test]
    fn a_play_names_a_title_and_a_unit_at_rest_names_none() {
        let mut keys = keys();

        let starting = keys.press("p");
        assert_eq!(
            status(&starting[0]).play,
            Some(Play {
                name: PLAY_NAME.to_string(),
                title: PLAY_TITLE.to_string(),
            })
        );

        let idle = keys.press("i");
        assert_eq!(status(&idle[0]).play, None);
    }

    #[test]
    fn the_end_of_a_film_returns_the_status_and_then_the_surface() {
        let pressed = keys().press("i");

        assert_eq!(pressed.len(), 2);
        assert_eq!(status(&pressed[0]).activity, Activity::Idle);
        assert_eq!(pressed[1], Moment::Present);
    }

    #[test]
    fn the_presence_key_disconnects_the_remote_and_connects_it_again() {
        let mut keys = keys();

        let gone = keys.press("d");
        assert_eq!(status(&gone[0]).components[2].connected, Some(false));

        let back = keys.press("d");
        assert_eq!(status(&back[0]).components[2].connected, Some(true));
    }

    #[test]
    fn focus_marks_the_remote_and_beats_its_marker() {
        let mut keys = keys();

        let landed = keys.press("f");
        assert_eq!(landed.len(), 2);
        assert_eq!(status(&landed[0]).components[2].focused, Some(true));
        assert_eq!(landed[1], Moment::Focus { remote: 0 });

        // The cycle press that wraps onto the unit already focused beats it
        // again.
        assert_eq!(keys.press("f")[1], Moment::Focus { remote: 0 });
    }

    #[test]
    fn focus_cycling_away_stops_marking_the_remote_and_beats_nothing() {
        let mut keys = keys();
        keys.press("f");

        let left = keys.press("g");

        assert_eq!(left.len(), 1);
        assert_eq!(status(&left[0]).components[2].focused, None);
    }

    #[test]
    fn the_sleep_key_plays_the_two_edges_of_the_fade_in_turn() {
        let mut keys = keys();

        assert_eq!(keys.press("s"), [Moment::Sleep], "the quiet window ran out");
        assert_eq!(keys.press("s"), [Moment::Wake], "a press woke the screen");
    }

    #[test]
    fn a_volume_key_steps_the_level_as_a_press() {
        let mut keys = keys();

        assert_eq!(
            keys.press("9"),
            [Moment::Level {
                volume: Volume {
                    level: UNITY_LEVEL - VOLUME_STEP,
                    muted: false,
                },
                pressed: true,
            }]
        );
        assert_eq!(
            keys.press("0"),
            [Moment::Level {
                volume: Volume {
                    level: UNITY_LEVEL,
                    muted: false,
                },
                pressed: true,
            }]
        );
    }

    #[test]
    fn the_level_stays_inside_the_range_the_operator_holds_it_in() {
        let mut keys = keys();
        for _ in 0..30 {
            keys.press("9");
        }
        assert_eq!(keys.volume.level, MIN_LEVEL);

        for _ in 0..30 {
            keys.press("0");
        }
        assert_eq!(keys.volume.level, UNITY_LEVEL);
    }

    #[test]
    fn the_mute_key_carries_the_muted_state_on_the_same_indicator() {
        let mut keys = keys();

        assert_eq!(
            keys.press("m"),
            [Moment::Level {
                volume: Volume {
                    level: UNITY_LEVEL,
                    muted: true,
                },
                pressed: true,
            }]
        );
        assert_eq!(
            keys.press("m"),
            [Moment::Level {
                volume: Volume {
                    level: UNITY_LEVEL,
                    muted: false,
                },
                pressed: true,
            }]
        );
    }

    #[test]
    fn a_key_this_module_does_not_bind_carries_nothing() {
        let mut keys = keys();
        assert!(keys.press("up").is_empty());
        assert!(keys.press("z").is_empty());
        assert_eq!(keys, self::keys());
    }

    #[test]
    fn the_preview_binds_every_key_the_legend_names_but_the_harness_key() {
        let mut keys = keys();
        let bound_to_nothing: Vec<&str> = LEGEND
            .iter()
            .flat_map(|(key, _)| key.split('/'))
            .filter(|key| keys.press(key).is_empty())
            .collect();

        assert_eq!(bound_to_nothing, [crate::harness::QUIT]);
    }

    #[test]
    fn the_legend_line_reads_each_key_beside_what_it_does() {
        assert_eq!(
            legend_line(),
            "p play starts    o playing    i film ends    d presence    f focus    \
             g unfocus    s sleep    9/0 volume    m mute    q quit"
        );
    }
}
