// The unit as the screen holds it: what the environment seeded, and what the
// bus has said since.
//
// Every field is a fact or the second a moment landed. No animation keeps its
// own state here, because each one is a function of the wall clock and the
// moments before it. A view reads the seconds below against the clock it is
// drawn at, so a captured frame is reproducible and a dropped frame is
// harmless.
//
// A moment that turns an ease around records where that ease stood as well as
// the second it landed. The ease that follows leaves from there, so a press
// that lands in the middle of a fade never makes the screen jump. The reading
// comes from the element that draws the curve, so one module states each ease
// and this one states the moments.

use crate::idle::{energy, identity, shade, volume};
use media_screen::Moment;
use media_screen::status::{Activity, Component, Status};
use media_screen::volume::Volume;

/// One part of the unit: a screen, a set of speakers, or a controller.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Part {
    pub name: String,
    /// `display`, `sink`, or `remote`. A part the environment seeded carries
    /// none, because the seed names the parts and nothing else.
    pub kind: String,
    /// The presence of a part that has any. `None` is a part with no live
    /// state, which draws at full brightness always.
    pub connected: Option<bool>,
    /// Whether the focus mark names this part. The seed carries no focus, so no
    /// marker draws before the first status.
    pub focused: bool,
    /// The second this part last came back, from a status that carried it from
    /// disconnected to connected. The identity block flashes the name white
    /// from here.
    pub returned: Option<f64>,
    /// The second this part last went away, from a status that carried it from
    /// connected to disconnected. The identity block eases the name down to the
    /// dim alpha from here, so a controller that disconnects leaves over the
    /// same 400 ms it returns over.
    pub left: Option<f64>,
    /// The second a focus moment last named this part. The marker beats from
    /// here. The status says whether the marker draws at all, and the moment
    /// carries the timing, so a beat that lands before the status still shows.
    pub marked: Option<f64>,
    /// The name's brightness at `returned` or `left`, so a part that
    /// flips presence mid-ease turns around from the level it stood at
    /// rather than jumping to one end.
    pub brightness_from: f32,
    /// The name's white flash at `returned`, so a second return inside
    /// one flash climbs from the level it holds.
    pub flash_from: f32,
    /// The marker's beat at `marked`, so a second focus moment inside
    /// one beat climbs from the level it holds.
    pub focus_from: f32,
}

/// The shade over the whole screen: which way it moves, and the second it
/// started. It eases down over 4000 ms and up over 400 ms.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Shade {
    pub down: bool,
    pub since: f64,
    /// The cover at that second, from 0 clear to 1 opaque. A press that lands
    /// while the screen still fades to black clears it from the grey it
    /// reached, and not from black.
    pub from: f64,
}

/// The ramp the energy runs, from the moment the activity last changed.
///
/// The target and the duration follow from the activity the unit holds now, so
/// this is the other half: the second the ramp started, the energy it started
/// from, and the animation clock the mark had reached by then. The clock is
/// recorded rather than counted again, because it is an integral over every
/// ramp before this one, and the unit holds none of those ramps.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Ramp {
    pub since: f64,
    pub from: f64,
    pub phase: f64,
}

/// The unit the client draws.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Unit {
    /// The unit's friendly name, seeded from `IDLE_PLAYER_NAME` and replaced by
    /// the first status.
    pub name: String,
    /// The parts, in the order the status lists them: the display, then each
    /// sink, then each remote.
    pub parts: Vec<Part>,
    pub activity: Activity,
    /// The energy's ramp, which starts over on every change of activity.
    pub ramp: Ramp,
    /// The title of the last `Play` the unit named. It stays after the `Play`
    /// ends, because the activity line fades out with the mark's motion rather
    /// than vanishing on the frame the activity changes.
    pub title: Option<String>,
    pub volume: Volume,
    /// The second of the last volume press. The volume row draws from here, and
    /// the broker's catch-up sets no second, so a pod that starts draws no row.
    pub pressed: Option<f64>,
    /// The row's own fade at that second, from 0 off screen to 1 full. A press
    /// that lands while the row fades out lifts it from where it stands, so a
    /// run of presses holds one row on screen instead of blinking it.
    pub pressed_from: f64,
    /// The shade, once a shade moment has arrived. A client that has read none
    /// draws no shade.
    pub shade: Option<Shade>,
    /// The second a new Wayland surface came up after a `present`. The mark
    /// starts its arrival motion from here.
    pub presented: Option<f64>,
}

impl Unit {
    /// The unit before the broker answers, from the seeds the operator set. A
    /// seeded part reports no presence, so the identity block draws every name
    /// at full brightness until the first status.
    pub fn seeded(name: String, components: Vec<String>) -> Self {
        Self {
            name,
            parts: components
                .into_iter()
                .map(|name| Part {
                    name,
                    ..Part::default()
                })
                .collect(),
            ..Self::default()
        }
    }

    /// Fold one moment in, at `at` seconds on the screen's clock. Every moment
    /// the unit holds arrives here, so one path carries what the broker says.
    pub fn fold(&mut self, moment: Moment, at: f64) {
        match moment {
            Moment::Status(status) => self.receive(status, at),
            Moment::Level { volume, pressed } => {
                if pressed {
                    self.pressed_from = volume::fade(self, at);
                    self.pressed = Some(at);
                }
                self.volume = volume;
            }
            Moment::Sleep => self.cover(true, at),
            Moment::Wake => self.cover(false, at),
            Moment::Focus { remote } => self.mark(remote, at),
            // A `present` asks for a new Wayland surface, which the harness
            // owns. `presented` records the second the new one came up.
            Moment::Present => {}
            // A press is the client's own to answer, and
            // `Client::receive` reads it before this fold. The stock
            // idle screen draws no list, so the unit changes for none
            // of them.
            Moment::Press(_) => {}
        }
    }

    /// Ease the shade toward black or toward clear.
    ///
    /// An ease already running that way runs on, and a cover already at that
    /// end stays there. The idle command pod sends a wake on every press, so
    /// without that rule a run of presses would restart the ease and hold the
    /// screen dim for as long as the presses keep arriving.
    fn cover(&mut self, down: bool, at: f64) {
        let running = match self.shade {
            Some(shade) => shade.down == down,
            None => !down,
        };
        if running {
            return;
        }

        self.shade = Some(Shade {
            down,
            since: at,
            from: shade::cover(self, at),
        });
    }

    /// Replace the whole unit with one status. A part that keeps its name keeps
    /// the seconds it drew from, so a status that changed one field does not
    /// restart every fade.
    fn receive(&mut self, status: Status, at: f64) {
        if !status.display_name.is_empty() {
            self.name = status.display_name;
        }
        // The operator publishes a status on every pass over the `Player`, and
        // a repeated one carries the activity the unit already holds. A ramp
        // that started over on each of those would stretch for as long as the
        // statuses keep arriving and never reach its target.
        if status.activity != self.activity {
            self.ramp = energy::ramp(self, status.activity, at);
        }
        self.activity = status.activity;
        if let Some(play) = &status.play {
            let title = [&play.title, &play.name]
                .into_iter()
                .find(|text| !text.is_empty());
            if let Some(title) = title {
                self.title = Some(title.clone());
            }
        }

        let previous = std::mem::take(&mut self.parts);
        self.parts = status
            .components
            .into_iter()
            .filter(|component| !component.name.is_empty())
            .map(|component| {
                let was = previous.iter().find(|part| part.name == component.name);
                part(component, was, at)
            })
            .collect();
    }

    /// Beat one controller's marker. `remote` counts the parts of kind
    /// `remote` in the order the status lists them. An index that names no
    /// controller changes nothing, because a moment can arrive for a controller
    /// this unit's status has not listed yet.
    fn mark(&mut self, remote: usize, at: f64) {
        if let Some(part) = self
            .parts
            .iter_mut()
            .filter(|part| part.kind == Component::REMOTE)
            .nth(remote)
        {
            let from = identity::focus(part, at);
            part.focus_from = from;
            part.marked = Some(at);
        }
    }
}

/// Whether a part draws at full brightness. A part that reports no presence
/// draws lit, because a part that cannot be absent must not read as
/// present-for-now. Only a part that reports it is away draws dim.
fn lit(connected: Option<bool>) -> bool {
    connected != Some(false)
}

/// One part of a status, against the part of that name the unit already held.
/// A part that changed presence takes the second it changed, and the identity
/// block eases its line from there.
///
/// The two seconds answer two different questions. `left` is the moment a lit
/// part went away, and a part that reported no presence at all is lit, so a
/// controller the environment seeded eases down the first time a status says
/// it is away. `returned` is the moment a part the status called away came
/// back connected, which is the flash that tells a person the controller
/// returned.
///
/// A part this unit has not listed before takes neither second, so a
/// controller that is already away when the first status lands draws dim
/// without easing there.
///
/// A part that turns also records the level the identity block drew it
/// at, read through that block's own curve, so the ease that follows
/// leaves from there. A part that did not turn keeps the levels it
/// already held.
fn part(component: Component, was: Option<&Part>, at: f64) -> Part {
    let went_away = was.is_some_and(|was| lit(was.connected)) && !lit(component.connected);
    let came_back =
        was.is_some_and(|was| was.connected == Some(false)) && component.connected == Some(true);
    let brightness_from = match was {
        Some(was) if went_away || came_back => identity::brightness(was, at),
        Some(was) => was.brightness_from,
        None => 0.0,
    };
    let flash_from = match was {
        Some(was) if came_back => identity::flash(was, at),
        Some(was) => was.flash_from,
        None => 0.0,
    };

    Part {
        name: component.name,
        kind: component.kind,
        connected: component.connected,
        focused: component.focused == Some(true),
        returned: came_back
            .then_some(at)
            .or_else(|| was.and_then(|was| was.returned)),
        left: went_away
            .then_some(at)
            .or_else(|| was.and_then(|was| was.left)),
        marked: was.and_then(|was| was.marked),
        brightness_from,
        flash_from,
        focus_from: was.map_or(0.0, |was| was.focus_from),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use media_screen::status::Play;

    fn seeded() -> Unit {
        Unit::seeded(
            "The Den".into(),
            vec!["The screen".into(), "The speakers".into()],
        )
    }

    fn component(name: &str, kind: &str, connected: Option<bool>) -> Component {
        Component {
            name: name.into(),
            kind: kind.into(),
            connected,
            focused: None,
        }
    }

    fn status(components: Vec<Component>) -> Status {
        Status {
            display_name: "The Den".into(),
            activity: Activity::Idle,
            play: None,
            components,
        }
    }

    #[test]
    fn the_seeds_name_the_unit_and_its_parts() {
        let unit = seeded();
        assert_eq!(unit.name, "The Den");
        assert_eq!(unit.parts.len(), 2);
        assert_eq!(unit.parts[0].name, "The screen");
        assert_eq!(unit.parts[0].connected, None);
        assert!(!unit.parts[0].focused);
    }

    #[test]
    fn the_first_status_replaces_the_whole_block() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(Status {
                display_name: "The Living Room".into(),
                activity: Activity::Starting,
                play: None,
                components: vec![component("A remote", "remote", Some(true))],
            }),
            1.0,
        );

        assert_eq!(unit.name, "The Living Room");
        assert_eq!(unit.activity, Activity::Starting);
        assert_eq!(unit.parts.len(), 1);
        assert_eq!(unit.parts[0].kind, "remote");
    }

    #[test]
    fn a_status_with_no_display_name_keeps_the_name_the_unit_has() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(Status {
                display_name: String::new(),
                ..status(Vec::new())
            }),
            1.0,
        );
        assert_eq!(unit.name, "The Den");
    }

    #[test]
    fn a_part_with_no_name_draws_no_line() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![
                component("", "sink", None),
                component("The speakers", "sink", None),
            ])),
            1.0,
        );
        assert_eq!(unit.parts.len(), 1);
        assert_eq!(unit.parts[0].name, "The speakers");
    }

    #[test]
    fn a_part_that_came_back_takes_the_second_it_returned() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            1.0,
        );
        assert_eq!(unit.parts[0].returned, None);

        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            4.0,
        );
        assert_eq!(unit.parts[0].returned, Some(4.0));

        // A later status that changed nothing does not flash the part again.
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            9.0,
        );
        assert_eq!(unit.parts[0].returned, Some(4.0));
    }

    #[test]
    fn a_part_that_went_away_takes_the_second_it_left() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        assert_eq!(unit.parts[0].left, None);

        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            4.0,
        );
        assert_eq!(unit.parts[0].left, Some(4.0));

        // A later status that changed nothing does not start the fade again.
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            9.0,
        );
        assert_eq!(unit.parts[0].left, Some(4.0));
    }

    #[test]
    fn a_part_the_seed_named_takes_the_second_it_left() {
        let mut unit = Unit::seeded("The Den".into(), vec!["A remote".into()]);
        assert_eq!(unit.parts[0].connected, None);

        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            2.0,
        );
        assert_eq!(unit.parts[0].left, Some(2.0));
    }

    #[test]
    fn a_part_that_came_back_mid_fade_records_the_level_it_stood_at() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            4.0,
        );
        assert_eq!(unit.parts[0].brightness_from, 1.0);

        // The return lands 200 ms into the 400 ms fade, so the name
        // stands at half and the ease up leaves from there.
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            4.2,
        );
        assert_eq!(unit.parts[0].returned, Some(4.2));
        assert_eq!(unit.parts[0].brightness_from, 0.5);
    }

    #[test]
    fn a_part_that_did_not_turn_keeps_the_level_it_already_recorded() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            4.0,
        );
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            9.0,
        );
        assert_eq!(unit.parts[0].brightness_from, 1.0);
    }

    #[test]
    fn a_focus_moment_inside_a_beat_records_the_level_the_marker_held() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );

        unit.fold(Moment::Focus { remote: 0 }, 3.0);
        assert_eq!(unit.parts[0].focus_from, 0.0);

        // The second moment lands 250 ms into the beat's 500 ms fall, so
        // the marker holds half and the next beat climbs from there.
        unit.fold(Moment::Focus { remote: 0 }, 3.37);
        assert_eq!(unit.parts[0].marked, Some(3.37));
        assert_eq!(unit.parts[0].focus_from, 0.5);
    }

    #[test]
    fn a_part_that_arrives_away_takes_no_second_to_fade_from() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            1.0,
        );
        assert_eq!(unit.parts[0].left, None);
    }

    #[test]
    fn a_part_that_arrives_connected_never_flashed() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        assert_eq!(unit.parts[0].returned, None);
    }

    #[test]
    fn the_title_comes_from_the_play_and_stays_after_it_ends() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Playing,
                play: Some(Play {
                    name: "den-tv-1".into(),
                    title: "A Film".into(),
                }),
                ..status(Vec::new())
            }),
            1.0,
        );
        assert_eq!(unit.title.as_deref(), Some("A Film"));

        unit.fold(Moment::Status(status(Vec::new())), 5.0);
        assert_eq!(unit.title.as_deref(), Some("A Film"));
        assert_eq!(unit.activity, Activity::Idle);
    }

    #[test]
    fn a_play_with_no_title_reads_its_object_name() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(Status {
                play: Some(Play {
                    name: "den-tv-1".into(),
                    title: String::new(),
                }),
                ..status(Vec::new())
            }),
            1.0,
        );
        assert_eq!(unit.title.as_deref(), Some("den-tv-1"));
    }

    #[test]
    fn the_catch_up_sets_the_level_and_a_press_sets_the_second() {
        let mut unit = seeded();
        let volume = Volume {
            level: 40,
            muted: false,
        };

        unit.fold(
            Moment::Level {
                volume,
                pressed: false,
            },
            1.0,
        );
        assert_eq!(unit.volume, volume);
        assert_eq!(unit.pressed, None);

        unit.fold(
            Moment::Level {
                volume: Volume {
                    level: 45,
                    muted: false,
                },
                pressed: true,
            },
            2.5,
        );
        assert_eq!(unit.volume.level, 45);
        assert_eq!(unit.pressed, Some(2.5));
    }

    #[test]
    fn the_shade_holds_the_way_it_moves_and_the_second_it_started() {
        let mut unit = seeded();
        assert_eq!(unit.shade, None);

        unit.fold(Moment::Sleep, 30.0);
        assert_eq!(
            unit.shade,
            Some(Shade {
                down: true,
                since: 30.0,
                from: 0.0
            })
        );

        // The screen reached black at 34.0, four seconds after the sleep, so
        // the wake clears from a full cover.
        unit.fold(Moment::Wake, 41.5);
        assert_eq!(
            unit.shade,
            Some(Shade {
                down: false,
                since: 41.5,
                from: 1.0
            })
        );
    }

    #[test]
    fn a_press_during_the_fade_to_black_clears_from_the_grey_it_reached() {
        let mut unit = seeded();
        unit.fold(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 12.0);

        // Halfway through the four seconds the cover stands at half.
        assert_eq!(
            unit.shade,
            Some(Shade {
                down: false,
                since: 12.0,
                from: 0.5
            })
        );
    }

    #[test]
    fn a_run_of_presses_holds_one_ease_rather_than_restarting_it() {
        let mut unit = seeded();
        unit.fold(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 12.0);
        unit.fold(Moment::Wake, 12.2);

        assert_eq!(unit.shade.map(|shade| shade.since), Some(12.0));
    }

    #[test]
    fn a_wake_on_a_clear_screen_starts_no_ease() {
        let mut unit = seeded();
        unit.fold(Moment::Wake, 3.0);
        assert_eq!(unit.shade, None);
    }

    #[test]
    fn the_ramp_starts_over_on_a_change_of_activity_and_not_on_a_repeat() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..status(Vec::new())
            }),
            2.0,
        );
        assert_eq!(unit.ramp.since, 2.0);

        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..status(Vec::new())
            }),
            2.5,
        );
        assert_eq!(unit.ramp.since, 2.0);

        unit.fold(Moment::Status(status(Vec::new())), 3.2);
        assert_eq!(unit.ramp.since, 3.2);
        assert_eq!(unit.ramp.from, 1.0);
    }

    #[test]
    fn a_press_during_the_fade_out_lifts_the_row_from_where_it_stands() {
        let mut unit = seeded();
        let volume = Volume {
            level: 40,
            muted: false,
        };

        unit.fold(
            Moment::Level {
                volume,
                pressed: false,
            },
            0.0,
        );
        unit.fold(
            Moment::Level {
                volume,
                pressed: true,
            },
            1.0,
        );
        assert_eq!(unit.pressed_from, 0.0);

        // The row leaves four seconds after the press, over 600 ms, so at 5.3
        // seconds it stands halfway out.
        unit.fold(
            Moment::Level {
                volume,
                pressed: true,
            },
            5.3,
        );
        assert_eq!(unit.pressed, Some(5.3));
        assert!(
            (unit.pressed_from - 0.5).abs() < 1e-9,
            "{}",
            unit.pressed_from
        );
    }

    #[test]
    fn a_focus_moment_beats_the_controller_it_counted_to() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![
                component("The screen", "display", None),
                component("A remote", "remote", Some(true)),
                component("Another remote", "remote", Some(true)),
            ])),
            1.0,
        );

        unit.fold(Moment::Focus { remote: 1 }, 3.0);

        assert_eq!(unit.parts[1].marked, None);
        assert_eq!(unit.parts[2].marked, Some(3.0));
    }

    #[test]
    fn a_focus_moment_that_names_no_controller_changes_nothing() {
        let mut unit = seeded();
        let before = unit.clone();
        unit.fold(Moment::Focus { remote: 4 }, 3.0);
        assert_eq!(unit, before);
    }

    #[test]
    fn a_part_that_keeps_its_name_keeps_the_seconds_it_draws_from() {
        let mut unit = seeded();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        unit.fold(Moment::Focus { remote: 0 }, 2.0);

        unit.fold(
            Moment::Status(status(vec![Component {
                focused: Some(true),
                ..component("A remote", "remote", Some(true))
            }])),
            3.0,
        );

        assert_eq!(unit.parts[0].marked, Some(2.0));
        assert!(unit.parts[0].focused);
    }

    #[test]
    fn a_present_leaves_the_unit_alone() {
        let mut unit = seeded();
        let before = unit.clone();
        unit.fold(Moment::Present, 3.0);
        assert_eq!(unit, before);
    }
}
