//! The preview keys, and the legend that lists them on the screen.
//!
//! On a cluster the [`Moment`] values come from the bus and from
//! `media-screen`. On a workstation `local/idle` sets `IDLE_PREVIEW=1`, and
//! each key press here builds the moments instead. The client passes them to
//! `Client::receive`, the same call that bus messages go through, so a person
//! can try every animation of the screen against the real handlers.

use iced_winit::core::Point;

use super::{Frame, Layout};
use crate::look;
use media_screen::Moment;
use media_screen::status::{Activity, Component, Play, Status};
use media_screen::volume::{MAX_LEVEL, MIN_LEVEL, Volume};

/// The kind of every part except the last one, the remote. The identity block
/// draws a screen and a speaker the same way, with no presence, so the
/// preview uses one kind for both.
const SINK: &str = "sink";

/// The battery percentage of the preview's remote, so the identity block
/// draws a charge level beside its name.
const PREVIEW_BATTERY: i64 = 62;

/// The `Play` in the preview status while one is starting or running.
const PLAY_NAME: &str = "sailing";
const PLAY_TITLE: &str = "Sailing";

/// The index of the remote in a `Focus` moment. The index counts the remotes
/// in the order the status lists them, and the preview lists one.
const REMOTE: usize = 0;

/// How far one volume key press moves the level, on a range of 0.0 to 1.0.
const VOLUME_STEP: f64 = 0.05;

/// The fake status of the unit that the keys change. Each key changes one
/// field and returns the complete status, as the operator republishes the
/// whole status after any change.
#[derive(Debug, Clone, PartialEq)]
pub struct Keys {
    /// The unit's name and its parts, from the environment variables the
    /// operator sets on the idle container. The operator lists the display,
    /// then the sinks, then the remotes, so the preview treats the last part
    /// as the remote.
    name: String,
    parts: Vec<String>,
    activity: Activity,
    /// Whether the remote is connected.
    connected: bool,
    /// Whether the status marks the remote focused.
    focused: bool,
    /// Whether the last `s` press put the screen to sleep, so the next one
    /// wakes it.
    asleep: bool,
    volume: Volume,
}

impl Keys {
    /// The keys for the unit that the environment describes, with the remote
    /// connected, nothing playing, and the screen awake.
    pub fn before_first_status(name: String, parts: Vec<String>) -> Self {
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

    /// The moments that one key press produces, in the order the bus would
    /// send them. A key that this module does not bind produces none.
    pub fn press(&mut self, key: &str) -> Vec<Moment> {
        match key {
            "p" => vec![self.doing(Activity::Starting)],
            "o" => vec![self.doing(Activity::Playing)],
            // The end of a film.
            "i" => vec![self.doing(Activity::Idle)],
            "d" => {
                self.connected = !self.connected;
                vec![self.status()]
            }
            // Focus moves to this unit. The status marks the remote, and the
            // `Focus` moment pulses its marker. A later press pulses it again,
            // as a focus cycle that wraps onto the focused unit does.
            "f" => {
                self.focused = true;
                vec![self.status(), Moment::Focus { remote: REMOTE }]
            }
            // Focus moves to another unit, so the marker disappears.
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

    fn doing(&mut self, activity: Activity) -> Moment {
        self.activity = activity;
        self.status()
    }

    /// The status as the operator would publish it now. It has a `Play` only
    /// while one is starting or running.
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
                        battery: Some(PREVIEW_BATTERY),
                        focused: self.focused.then_some(true),
                    }
                } else {
                    Component {
                        name: name.clone(),
                        kind: SINK.to_string(),
                        connected: None,
                        battery: None,
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

    /// Moves the level by `step`, within the range the operator allows.
    fn step(&mut self, step: f64) -> Moment {
        self.volume.level = (self.volume.level + step).clamp(MIN_LEVEL, MAX_LEVEL);
        self.level()
    }

    /// The level with `pressed` set, so the volume row shows. On the bus,
    /// the first level of a session is the broker's retained message, which
    /// no person caused, and it is not pressed.
    fn level(&self) -> Moment {
        Moment::Level {
            volume: self.volume,
            pressed: true,
        }
    }
}

/// Each key and its description, in the order the legend shows them.
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
    // The harness, not this module, reads the quit key.
    (crate::harness::QUIT, "quit"),
];

/// The space between pairs, wider than the space between a key and its
/// description.
const LEGEND_GAP: &str = "    ";

fn legend_line() -> String {
    LEGEND
        .iter()
        .map(|(key, does)| format!("{key} {does}"))
        .collect::<Vec<String>>()
        .join(LEGEND_GAP)
}

/// Draws the legend at the bottom-right corner, opposite the identity block
/// at the bottom-left.
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
        Keys::before_first_status(
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
            panic!("the press produced {moment:?}, want a Moment::Status");
        };
        status
    }

    #[test]
    fn the_status_has_the_seeded_name_and_parts() {
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
    fn a_unit_with_no_parts_has_no_components() {
        let mut keys = Keys::before_first_status("Studio Lab".to_string(), Vec::new());
        assert!(status(&keys.press("d")[0]).components.is_empty());
    }

    #[test]
    fn the_play_keys_set_starting_playing_and_idle() {
        let mut keys = keys();

        assert_eq!(status(&keys.press("p")[0]).activity, Activity::Starting);
        assert_eq!(status(&keys.press("o")[0]).activity, Activity::Playing);
        assert_eq!(status(&keys.press("i")[0]).activity, Activity::Idle);
    }

    #[test]
    fn the_status_has_a_play_while_starting_and_none_at_rest() {
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
    fn the_film_end_key_sends_only_a_status() {
        let pressed = keys().press("i");

        assert_eq!(pressed.len(), 1);
        assert_eq!(status(&pressed[0]).activity, Activity::Idle);
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
    fn the_focus_key_marks_the_remote_and_pulses_its_marker() {
        let mut keys = keys();

        let landed = keys.press("f");
        assert_eq!(landed.len(), 2);
        assert_eq!(status(&landed[0]).components[2].focused, Some(true));
        assert_eq!(landed[1], Moment::Focus { remote: 0 });

        // A second press pulses the marker again.
        assert_eq!(keys.press("f")[1], Moment::Focus { remote: 0 });
    }

    #[test]
    fn the_unfocus_key_unmarks_the_remote_and_sends_no_focus_moment() {
        let mut keys = keys();
        keys.press("f");

        let left = keys.press("g");

        assert_eq!(left.len(), 1);
        assert_eq!(status(&left[0]).components[2].focused, None);
    }

    #[test]
    fn the_sleep_key_alternates_sleep_and_wake() {
        let mut keys = keys();

        assert_eq!(keys.press("s"), [Moment::Sleep], "the quiet window ran out");
        assert_eq!(keys.press("s"), [Moment::Wake], "a press woke the screen");
    }

    #[test]
    fn a_volume_key_steps_the_level_with_pressed_set() {
        let mut keys = keys();

        assert_eq!(
            keys.press("9"),
            [Moment::Level {
                volume: Volume {
                    level: MAX_LEVEL - VOLUME_STEP,
                    muted: false,
                    ..Volume::default()
                },
                pressed: true,
            }]
        );
        assert_eq!(
            keys.press("0"),
            [Moment::Level {
                volume: Volume {
                    level: MAX_LEVEL,
                    muted: false,
                    ..Volume::default()
                },
                pressed: true,
            }]
        );
    }

    #[test]
    fn the_level_stays_within_min_and_max() {
        let mut keys = keys();
        for _ in 0..30 {
            keys.press("9");
        }
        assert_eq!(keys.volume.level, MIN_LEVEL);

        for _ in 0..30 {
            keys.press("0");
        }
        assert_eq!(keys.volume.level, MAX_LEVEL);
    }

    #[test]
    fn the_mute_key_toggles_muted_in_a_pressed_level() {
        let mut keys = keys();

        assert_eq!(
            keys.press("m"),
            [Moment::Level {
                volume: Volume {
                    level: MAX_LEVEL,
                    muted: true,
                    ..Volume::default()
                },
                pressed: true,
            }]
        );
        assert_eq!(
            keys.press("m"),
            [Moment::Level {
                volume: Volume {
                    level: MAX_LEVEL,
                    muted: false,
                    ..Volume::default()
                },
                pressed: true,
            }]
        );
    }

    #[test]
    fn an_unbound_key_produces_no_moments() {
        let mut keys = keys();
        assert!(keys.press("up").is_empty());
        assert!(keys.press("z").is_empty());
        assert_eq!(keys, self::keys());
    }

    #[test]
    fn every_legend_key_except_quit_is_bound() {
        let mut keys = keys();
        let bound_to_nothing: Vec<&str> = LEGEND
            .iter()
            .flat_map(|(key, _)| key.split('/'))
            .filter(|key| keys.press(key).is_empty())
            .collect();

        assert_eq!(bound_to_nothing, [crate::harness::QUIT]);
    }

    #[test]
    fn the_legend_line_lists_each_key_with_its_description() {
        assert_eq!(
            legend_line(),
            "p play starts    o playing    i film ends    d presence    f focus    \
             g unfocus    s sleep    9/0 volume    m mute    q quit"
        );
    }
}
