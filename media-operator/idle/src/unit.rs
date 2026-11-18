// The state of one player as the idle screen draws it. It starts from the
// player name and part names that the operator passes in environment
// variables, and each `Moment` from `media-screen` updates it.
//
// The unit records when each change happened, and no animation state. The
// `idle` modules compute every animation from the current time and those
// times, so a captured frame is the same on every run and a skipped frame
// costs nothing. When a change reverses an animation that is still running,
// the unit also records the animation's value at that time in a `*_from`
// field. The next animation starts from that value, so the screen does not
// jump.

use crate::idle::{energy, identity, shade, volume};
use media_screen::Moment;
use media_screen::status::{Activity, Component, Status};
use media_screen::volume::Volume;

/// One part of the unit: a screen, a set of speakers, or a controller.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Part {
    pub name: String,
    /// `display`, `sink`, or `remote`. Empty for a part created from the
    /// environment, which supplies names only.
    pub kind: String,
    /// Whether the part is connected. `None` when the status does not report
    /// it, as for a display or a sink, and then the name draws at full
    /// brightness.
    pub connected: Option<bool>,
    /// The charge the part reports, from 0 to 100. `idle::identity` draws
    /// it as a bar in the left margin. `None` when the part reports no
    /// charge, and then its line shows the name alone.
    pub battery: Option<i64>,
    /// Whether the status marks this part as focused, which draws the focus
    /// marker.
    pub focused: bool,
    /// The time of the status that reconnected this part. `idle::identity`
    /// flashes the name white from this time.
    pub returned: Option<f64>,
    /// The time of the status that reported this part disconnected after it
    /// was connected or reported nothing. `idle::identity` fades the name
    /// to its dim level from this time.
    pub left: Option<f64>,
    /// The time of the last `Focus` moment for this part, which starts the
    /// marker's beat. The beat shows even before a status marks the part
    /// focused.
    pub marked: Option<f64>,
    /// The name's brightness at `returned` or `left`. The next fade starts
    /// from it, so a part that reconnects or disconnects during a fade does
    /// not jump to full or dim.
    pub brightness_from: f32,
    /// The level of the name's white flash at `returned`, so a second
    /// reconnect during a flash starts from the level the flash reached.
    pub flash_from: f32,
    /// The level of the marker's beat at `marked`, so a second `Focus` during
    /// a beat starts from the level the beat reached.
    pub focus_from: f32,
}

/// The fade of the whole screen to black and back. `down` is true while the
/// screen fades to black and false while it clears. `since` is the time the
/// fade started. `idle::shade` sets how long each direction takes.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Shade {
    pub down: bool,
    pub since: f64,
    /// How dark the screen was at `since`, from 0 clear to 1 black, so a
    /// `Wake` during the fade to black clears from the darkness it reached.
    pub from: f64,
}

/// The energy ramp that started at the last change of activity.
///
/// The unit's `activity` sets the ramp's target energy and duration. This
/// records the time the ramp started, the energy at that time, and the mark's
/// animation clock at that time in `phase`. The clock is the integral of the
/// energy over every earlier ramp, and the unit keeps no earlier ramps, so it
/// stores the clock.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Ramp {
    pub since: f64,
    pub from: f64,
    pub phase: f64,
}

/// The time the unit's activity changed to `Idle`, and the activity it left.
///
/// After `Playing`, the mark's energy starts again from full, because the
/// mark was stopped and covered during playback. After any other activity
/// the mark is still moving on screen, so the ramp that the change of
/// activity started continues from the mark's current energy.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Arrival {
    pub at: f64,
    pub from: Activity,
}

/// The unit the client draws.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Unit {
    /// The unit's friendly name. It starts as `IDLE_PLAYER_NAME`, and the
    /// first status with a display name replaces it.
    pub name: String,
    /// The parts, in the order the status lists them: the display, then each
    /// sink, then each remote.
    pub parts: Vec<Part>,
    pub activity: Activity,
    pub ramp: Ramp,
    /// The title of the last `Play` in a status. It is kept after the `Play`
    /// ends, because the activity line fades out with the mark's motion and
    /// still draws the text during the fade.
    pub title: Option<String>,
    pub volume: Volume,
    /// The time of the last live volume message that showed the volume row.
    /// A live message is a change that a person made. The retained message
    /// that the broker sends on connect does not set it, so a pod that starts
    /// draws no row.
    pub pressed: Option<f64>,
    /// The row's fade level at `pressed`, from 0 off screen to 1 full. A
    /// change during the fade out fades the row in from that level, so a run
    /// of presses keeps the row on screen and it does not blink.
    pub pressed_from: f64,
    /// `None` until the first `Sleep`. While it is `None`, the screen draws
    /// no shade.
    pub shade: Option<Shade>,
    /// The last change of the activity to `Idle` from another activity.
    /// `Client::receive` sets it. `None` until the first such change.
    pub arrived: Option<Arrival>,
}

impl Unit {
    /// Creates the unit that the client draws before the first status. The
    /// parts have only names and report no presence, so `idle::identity`
    /// draws every name at full brightness until the first status.
    pub fn before_first_status(name: String, components: Vec<String>) -> Self {
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

    /// Applies one `Moment` to the unit. `at` is the time in seconds on the
    /// screen's clock.
    pub fn fold(&mut self, moment: Moment, at: f64) {
        match moment {
            Moment::Status(status) => self.receive(status, at),
            // A live level shows the volume row, unless the receiver shows
            // the level on the TV or only the indicator changed. See
            // `Volume::draws_after`. The unit stores the level in every case.
            Moment::Level { volume, pressed } => {
                if pressed && volume.draws_after(self.volume) {
                    self.pressed_from = volume::fade(self, at);
                    self.pressed = Some(at);
                }
                self.volume = volume;
            }
            Moment::Sleep => self.cover(true, at),
            Moment::Wake => self.cover(false, at),
            Moment::Focus { remote } => self.mark(remote, at),
            // `Client::receive` handles a press before this fold. The stock
            // idle screen draws no list, so a press changes nothing here.
            Moment::Press(_) => {}
            // The up-next offer is for the client that wrote the `Play`. The
            // stock idle screen writes none, so it ignores the offer.
            Moment::PlayNext(_) => {}
            // The stock idle screen owns no topic, so no message arrives for
            // it, and it has no retained state to republish when a bus
            // session starts.
            Moment::Message { .. } | Moment::Connected => {}
        }
    }

    /// Starts the shade fading to black (`down`) or clearing.
    ///
    /// A shade that already fades in that direction continues, and a `Wake`
    /// on a screen with no shade sets none, so a repeated `Sleep` or `Wake`
    /// does not restart the fade.
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

    /// Replaces the unit's state with one status. A part keeps the recorded
    /// times of the part with the same name, so a status that changes one
    /// field does not restart every fade.
    fn receive(&mut self, status: Status, at: f64) {
        if !status.display_name.is_empty() {
            self.name = status.display_name;
        }
        // Most statuses repeat the current activity. If each one restarted
        // the ramp, the ramp would not reach its target while statuses keep
        // arriving.
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

    /// Starts the marker beat on one controller. `remote` is an index among
    /// the parts of kind `remote`, in the order the status lists them. An
    /// index with no controller changes nothing, because a `Focus` can arrive
    /// for a controller that no status has listed yet.
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

/// Only `Some(false)` counts as absent, so a display or a sink, whose status
/// reports no `connected`, never draws dim.
fn is_present(connected: Option<bool>) -> bool {
    connected != Some(false)
}

/// Builds one part from its entry in a status and the part of the same name
/// in the previous status, if there was one.
///
/// `left` is set when a part that counted as present is now disconnected, so
/// a remote that only the environment named fades the first time a status
/// reports it disconnected. `returned` is set when a disconnected part is
/// now connected. A part with no earlier entry gets neither time, so a remote
/// that is already disconnected in the first status that lists it draws dim
/// with no fade.
///
/// When a part disconnects or reconnects, this records the brightness that
/// `idle::identity` drew at `at`, and on a reconnect the flash level, so
/// the next fade starts from there.
fn part(component: Component, was: Option<&Part>, at: f64) -> Part {
    let went_away =
        was.is_some_and(|was| is_present(was.connected)) && !is_present(component.connected);
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
        battery: component.battery,
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

    fn fresh_unit() -> Unit {
        Unit::before_first_status(
            "Player One".into(),
            vec!["The screen".into(), "The speakers".into()],
        )
    }

    fn component(name: &str, kind: &str, connected: Option<bool>) -> Component {
        Component {
            name: name.into(),
            kind: kind.into(),
            connected,
            battery: None,
            focused: None,
        }
    }

    fn status(components: Vec<Component>) -> Status {
        Status {
            display_name: "Player One".into(),
            activity: Activity::Idle,
            play: None,
            components,
        }
    }

    #[test]
    fn a_seeded_unit_has_the_given_name_and_parts_that_report_nothing() {
        let unit = fresh_unit();
        assert_eq!(unit.name, "Player One");
        assert_eq!(unit.parts.len(), 2);
        assert_eq!(unit.parts[0].name, "The screen");
        assert_eq!(unit.parts[0].connected, None);
        assert!(
            !unit.parts[0].focused,
            "the first part is focused, want unfocused"
        );
    }

    #[test]
    fn a_status_replaces_the_name_activity_and_parts() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(Status {
                display_name: "Player Two".into(),
                activity: Activity::Starting,
                play: None,
                components: vec![component("A remote", "remote", Some(true))],
            }),
            1.0,
        );

        assert_eq!(unit.name, "Player Two");
        assert_eq!(unit.activity, Activity::Starting);
        assert_eq!(unit.parts.len(), 1);
        assert_eq!(unit.parts[0].kind, "remote");
    }

    #[test]
    fn a_status_with_no_display_name_keeps_the_name_the_unit_has() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(Status {
                display_name: String::new(),
                ..status(Vec::new())
            }),
            1.0,
        );
        assert_eq!(unit.name, "Player One");
    }

    #[test]
    fn a_status_drops_a_part_with_no_name() {
        let mut unit = fresh_unit();
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
    fn a_status_that_reconnects_a_part_sets_its_returned_time() {
        let mut unit = fresh_unit();
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
    fn a_status_that_disconnects_a_part_sets_its_left_time() {
        let mut unit = fresh_unit();
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
    fn a_seeded_part_that_a_status_reports_away_sets_its_left_time() {
        let mut unit = Unit::before_first_status("Player One".into(), vec!["A remote".into()]);
        assert_eq!(unit.parts[0].connected, None);

        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            2.0,
        );
        assert_eq!(unit.parts[0].left, Some(2.0));
    }

    #[test]
    fn a_reconnect_during_the_fade_down_records_the_brightness_reached() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            4.0,
        );
        assert_eq!(unit.parts[0].brightness_from, 1.0);

        // The reconnect arrives 200 ms into the 400 ms fade down, so the
        // name is at half brightness and the fade up starts from there.
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            4.2,
        );
        assert_eq!(unit.parts[0].returned, Some(4.2));
        assert_eq!(unit.parts[0].brightness_from, 0.5);
    }

    #[test]
    fn a_repeated_status_keeps_the_recorded_brightness() {
        let mut unit = fresh_unit();
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
    fn a_focus_during_a_beat_records_the_marker_level_reached() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );

        unit.fold(Moment::Focus { remote: 0 }, 3.0);
        assert_eq!(unit.parts[0].focus_from, 0.0);

        // The second focus arrives 250 ms into the 500 ms fall of the first
        // beat, so the marker is at half and the next beat rises from
        // there.
        unit.fold(Moment::Focus { remote: 0 }, 3.37);
        assert_eq!(unit.parts[0].marked, Some(3.37));
        assert_eq!(unit.parts[0].focus_from, 0.5);
    }

    #[test]
    fn a_part_first_reported_away_has_no_left_time() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(false))])),
            1.0,
        );
        assert_eq!(unit.parts[0].left, None);
    }

    #[test]
    fn a_part_first_reported_connected_has_no_returned_time() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            1.0,
        );
        assert_eq!(unit.parts[0].returned, None);
    }

    #[test]
    fn the_title_comes_from_the_play_and_stays_after_it_ends() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Playing,
                play: Some(Play {
                    name: "player-one-1".into(),
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
    fn a_play_with_no_title_uses_its_object_name() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(Status {
                play: Some(Play {
                    name: "player-one-1".into(),
                    title: String::new(),
                }),
                ..status(Vec::new())
            }),
            1.0,
        );
        assert_eq!(unit.title.as_deref(), Some("player-one-1"));
    }

    #[test]
    fn a_retained_level_sets_the_volume_and_a_live_level_sets_the_press_time() {
        let mut unit = fresh_unit();
        let volume = Volume {
            level: 0.4,
            muted: false,
            ..Volume::default()
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
                    level: 0.45,
                    muted: false,
                    ..Volume::default()
                },
                pressed: true,
            },
            2.5,
        );
        assert_eq!(unit.volume.level, 0.45);
        assert_eq!(unit.pressed, Some(2.5));
    }

    #[test]
    fn a_play_next_moment_changes_nothing_this_screen_draws() {
        let mut unit = fresh_unit();
        let before = unit.clone();

        unit.fold(Moment::PlayNext(br#"{"library":"shows"}"#.to_vec()), 2.0);

        assert_eq!(unit, before);
    }

    #[test]
    fn a_client_topic_message_and_a_bus_session_change_nothing_this_screen_draws() {
        let mut unit = fresh_unit();
        let before = unit.clone();

        unit.fold(
            Moment::Message {
                topic: "liken/library/screens/default/player-one/watching".into(),
                payload: b"person-a".to_vec(),
                retained: true,
            },
            2.0,
        );
        unit.fold(Moment::Connected, 2.0);

        assert_eq!(unit, before);
    }

    #[test]
    fn a_sleep_and_a_wake_record_the_shade_direction_and_start_time() {
        let mut unit = fresh_unit();
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

        // The fade to black takes 4 s, so the screen is fully black at 34.0
        // and the wake clears from full black.
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
    fn a_wake_during_the_fade_to_black_clears_from_the_darkness_reached() {
        let mut unit = fresh_unit();
        unit.fold(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 12.0);

        // Halfway through the 4 s fade the screen is half dark.
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
    fn repeated_wakes_do_not_restart_the_clearing_fade() {
        let mut unit = fresh_unit();
        unit.fold(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 12.0);
        unit.fold(Moment::Wake, 12.2);

        assert_eq!(unit.shade.map(|shade| shade.since), Some(12.0));
    }

    #[test]
    fn a_wake_on_a_clear_screen_sets_no_shade() {
        let mut unit = fresh_unit();
        unit.fold(Moment::Wake, 3.0);
        assert_eq!(unit.shade, None);
    }

    #[test]
    fn the_ramp_starts_over_on_a_change_of_activity_and_not_on_a_repeat() {
        let mut unit = fresh_unit();
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
    fn a_level_change_during_the_row_fade_out_fades_in_from_the_current_level() {
        let mut unit = fresh_unit();
        let volume = Volume {
            level: 0.4,
            muted: false,
            ..Volume::default()
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

        // The row starts to fade out 4 s after the press at 1.0 and takes
        // 600 ms, so at 5.3 it is half visible.
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
            "pressed_from is {}, want 0.5",
            unit.pressed_from
        );
    }

    #[test]
    fn a_focus_moment_beats_the_remote_at_its_index_among_remotes() {
        let mut unit = fresh_unit();
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
    fn a_focus_moment_with_no_matching_controller_changes_nothing() {
        let mut unit = fresh_unit();
        let before = unit.clone();
        unit.fold(Moment::Focus { remote: 4 }, 3.0);
        assert_eq!(unit, before);
    }

    #[test]
    fn a_status_sets_and_clears_the_battery_of_a_part() {
        let mut unit = fresh_unit();
        unit.fold(
            Moment::Status(status(vec![Component {
                battery: Some(62),
                ..component("A remote", "remote", Some(true))
            }])),
            1.0,
        );
        assert_eq!(unit.parts[0].battery, Some(62));

        // The status no longer reports a level, so the part has none.
        unit.fold(
            Moment::Status(status(vec![component("A remote", "remote", Some(true))])),
            2.0,
        );
        assert_eq!(unit.parts[0].battery, None);
    }

    #[test]
    fn a_part_with_the_same_name_keeps_its_recorded_times() {
        let mut unit = fresh_unit();
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
        assert!(
            unit.parts[0].focused,
            "the first part is unfocused, want focused"
        );
    }
}
