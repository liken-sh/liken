//! Places the selected subtitle track so that its last line ends on the
//! house row at the house size, and moves its cues up while the display
//! is showing something at the foot of the frame.
//!
//! The command sidecar chooses the track, and a person can choose another
//! in the display's chooser. The display observes `track-list`, so it
//! sees the selected track whoever selected it, and it is the one program
//! that sets the properties that place a track: `sub-ass-override`,
//! `stretch-image-subs-to-screen`, `sub-scale`, and `sub-pos`.
//! `placement` computes their values for each kind of track.
//!
//! mpv draws subtitles in its own surface under the display's. At their
//! resting place at the foot of the frame, a cue would be under the
//! scrubber and the control strip, and a person who brings up the
//! display to check the time could not read it. So while the display
//! draws anything in the band at the foot of the frame, it lowers
//! `sub-pos` from the track's resting value so that the lowest line of a
//! cue ends `GAP` above the highest thing the display draws there. When
//! the display hides, `sub-pos` goes back to the resting value.
//!
//! The display uses `sub-pos` and not `sub-margin-y`, because
//! `sub-margin-y` moves only text tracks that have no style of their own,
//! such as SubRip. `sub-pos` moves all three kinds. libass moves each ASS
//! event that the script does not place itself, so a sign placed with
//! `\pos` stays on the thing it labels. mpv moves each bitmap of an image
//! track that starts in the lower half of its canvas.
//!
//! mpv does not animate `sub-pos`. So on each tick of the display's fade,
//! the display sends the position for the fade's current value, and the
//! subtitles move with the fade. When nothing changes, the display sends
//! nothing.

pub mod lift;
pub mod measurement;
pub mod placement;
pub mod script;

use serde_json::{Value, json};

use crate::film::Track;
use crate::ipc::Command;
use placement::{Override, PLAIN, Placement, Reason};

/// The properties that place a track, in the order that the display sends
/// them.
pub const OVERRIDE: &str = "sub-ass-override";
pub const STRETCH: &str = "stretch-image-subs-to-screen";
pub const SCALE: &str = "sub-scale";
pub const POSITION: &str = "sub-pos";

/// The value of each property that the display last sent to mpv.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Sent {
    ass_override: Override,
    stretch: bool,
    scale: f64,
    position: f64,
}

/// The track that the display last read a script header for, by the file
/// and the fields of `track-list` that identify a stream. mpv numbers the
/// tracks of each file from 1, so the next file's track 3 is another
/// track.
#[derive(Debug, Clone, Default, PartialEq)]
struct Selection {
    path: Option<String>,
    id: i64,
    codec: Option<String>,
    ff_index: Option<i64>,
    external_filename: Option<String>,
}

impl Selection {
    fn of(path: Option<&str>, track: &Track) -> Self {
        Self {
            path: path.map(str::to_string),
            id: track.id,
            codec: track.codec.clone(),
            ff_index: track.ff_index,
            external_filename: track.external_filename.clone(),
        }
    }

    fn is(&self, path: Option<&str>, track: &Track) -> bool {
        self.path.as_deref() == path
            && self.id == track.id
            && self.codec == track.codec
            && self.ff_index == track.ff_index
            && self.external_filename == track.external_filename
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct Subtitles {
    sent: Sent,
    /// The track and the placement of the last log line.
    logged: Option<(i64, Option<String>, Placement)>,
    band: Option<f32>,
    selection: Option<Selection>,
}

impl Default for Subtitles {
    /// The player sets none of the placing properties, so mpv starts with
    /// its defaults.
    fn default() -> Self {
        Self {
            sent: Sent {
                ass_override: PLAIN.ass_override,
                stretch: PLAIN.stretch,
                scale: PLAIN.scale,
                position: PLAIN.rest,
            },
            logged: None,
            band: None,
            selection: None,
        }
    }
}

/// What one call to `Subtitles::follow` returns.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Follow {
    /// A `set_property` for each value that differs from the one last
    /// sent.
    pub commands: Vec<Command>,
    /// A line when the placement changed and a line when the band
    /// changed, so a fade writes no line for each tick.
    pub logged: Vec<String>,
}

impl Subtitles {
    /// Returns true when the selected track is an ASS track that the
    /// display has not read a header for. mpv sends `sub-ass-extradata`
    /// when a client observes it and when mpv goes idle, and not when the
    /// selected track changes. So the display observes the property again
    /// for each ASS track that is selected, and mpv then sends its value.
    pub fn reads_script(&mut self, path: Option<&str>, track: Option<&Track>) -> bool {
        let unchanged = match (&self.selection, track) {
            (Some(selection), Some(track)) => selection.is(path, track),
            (None, None) => true,
            _ => false,
        };
        if unchanged {
            return false;
        }
        self.selection = track.map(|track| Selection::of(path, track));
        track.is_some_and(placement::is_ass)
    }

    /// `placed` is the selected track and its placement. It is `None` when
    /// no subtitle track is selected, and while a value that the placement
    /// needs has not arrived, and then the display sends nothing. `top` is
    /// the highest canvas row that the display draws on in the band, or
    /// `None` while the display is hidden.
    pub fn follow(
        &mut self,
        placed: Option<(&Track, Placement)>,
        top: Option<f32>,
        fade: f32,
    ) -> Follow {
        let Some((track, placement)) = placed else {
            return Follow::default();
        };
        let values = placement.values;
        let position = lift::position(values.rest, placement.lift, top, fade);
        let mut commands = Vec::new();
        if values.ass_override != self.sent.ass_override {
            commands.push(set(OVERRIDE, json!(values.ass_override.word())));
        }
        if values.stretch != self.sent.stretch {
            commands.push(set(STRETCH, json!(values.stretch)));
        }
        if values.scale != self.sent.scale {
            commands.push(set(SCALE, json!(values.scale)));
        }
        if position != self.sent.position {
            commands.push(set(POSITION, json!(position)));
        }
        self.sent = Sent {
            ass_override: values.ass_override,
            stretch: values.stretch,
            scale: values.scale,
            position,
        };

        let mut logged = Vec::new();
        let identity = (track.id, track.codec.clone(), placement);
        if self.logged.as_ref() != Some(&identity) {
            logged.push(placed_line(track, &placement));
            self.logged = Some(identity);
        }
        if top != self.band {
            self.band = top;
            logged.push(match top {
                Some(top) => format!(
                    "media-display: subtitles move above row {top} of the display, to {POSITION} {}",
                    lift::position(values.rest, placement.lift, Some(top), 1.0)
                ),
                None => format!(
                    "media-display: subtitles return to their place, {POSITION} {}",
                    values.rest
                ),
            });
        }
        Follow { commands, logged }
    }
}

fn set(name: &str, value: Value) -> Command {
    vec![json!("set_property"), json!(name), value]
}

fn placed_line(track: &Track, placement: &Placement) -> String {
    let values = placement.values;
    let state = match placement.unplaced {
        None => "ends on the house row",
        Some(_) => "is drawn as the disc placed it",
    };
    let cause = match placement.unplaced {
        None => String::new(),
        Some(Reason::Unmeasured) => {
            ", because the presentation has no image block for the track".to_string()
        }
        Some(Reason::Screen { width, height }) => {
            format!(", because the screen is {width}x{height}, not 16:9")
        }
    };
    format!(
        "media-display: subtitle track {}, codec {:?}, {state}, with {OVERRIDE} {}, {STRETCH} {}, {SCALE} {}, and {POSITION} {}{cause}",
        track.id,
        track.codec.as_deref().unwrap_or_default(),
        values.ass_override.word(),
        if values.stretch { "yes" } else { "no" },
        values.scale,
        values.rest,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use placement::{Lift, Values};

    fn track(id: i64, codec: &str) -> Track {
        Track {
            id,
            kind: "sub".to_string(),
            selected: true,
            codec: Some(codec.to_string()),
            ..Track::default()
        }
    }

    fn text() -> (Track, Placement) {
        (
            track(1, "subrip"),
            Placement {
                values: PLAIN,
                lift: Lift::Share,
                unplaced: None,
            },
        )
    }

    fn pgs() -> (Track, Placement) {
        (
            track(3, "hdmv_pgs_subtitle"),
            Placement {
                values: Values {
                    ass_override: Override::Scale,
                    stretch: true,
                    scale: 0.7541,
                    rest: 111.157,
                },
                lift: Lift::Rows,
                unplaced: None,
            },
        )
    }

    fn ass() -> (Track, Placement) {
        (
            track(2, "ass"),
            Placement {
                values: Values {
                    ass_override: Override::Force,
                    stretch: false,
                    scale: 1.0,
                    rest: 97.358,
                },
                lift: Lift::Share,
                unplaced: None,
            },
        )
    }

    fn follow(
        subtitles: &mut Subtitles,
        placed: &(Track, Placement),
        top: Option<f32>,
        fade: f32,
    ) -> Follow {
        subtitles.follow(Some((&placed.0, placed.1)), top, fade)
    }

    /// mpv starts with the plain values, so a plain text track sends
    /// nothing until the display lifts it.
    #[test]
    fn a_value_already_sent_is_not_sent_again() {
        let mut subtitles = Subtitles::default();
        assert_eq!(
            follow(&mut subtitles, &text(), None, 0.0).commands,
            Vec::<Command>::new()
        );
        assert_eq!(
            follow(&mut subtitles, &text(), Some(846.0), 0.0).commands,
            Vec::<Command>::new()
        );
        assert_eq!(
            follow(&mut subtitles, &text(), Some(846.0), 1.0).commands,
            [set(POSITION, json!(79.9))]
        );
        assert_eq!(
            follow(&mut subtitles, &text(), Some(846.0), 1.0).commands,
            Vec::<Command>::new()
        );
        assert_eq!(
            follow(&mut subtitles, &text(), None, 0.0).commands,
            [set(POSITION, json!(100.0))]
        );
    }

    /// Each step sends only the properties whose values change.
    #[test]
    fn a_new_track_sends_the_values_that_differ() {
        let mut subtitles = Subtitles::default();
        assert_eq!(
            follow(&mut subtitles, &pgs(), None, 0.0).commands,
            [
                set(STRETCH, json!(true)),
                set(SCALE, json!(0.7541)),
                set(POSITION, json!(111.157)),
            ]
        );
        assert_eq!(
            follow(&mut subtitles, &ass(), None, 0.0).commands,
            [
                set(OVERRIDE, json!("force")),
                set(STRETCH, json!(false)),
                set(SCALE, json!(1.0)),
                set(POSITION, json!(97.358)),
            ]
        );
        assert_eq!(
            follow(&mut subtitles, &text(), None, 0.0).commands,
            [set(OVERRIDE, json!("scale")), set(POSITION, json!(100.0))]
        );
    }

    /// With no track, or with a placement that waits for a value, the
    /// display leaves every property as it is.
    #[test]
    fn no_placement_sends_nothing() {
        let mut subtitles = Subtitles::default();
        follow(&mut subtitles, &pgs(), Some(846.0), 1.0);
        assert_eq!(subtitles.follow(None, Some(846.0), 1.0), Follow::default());
        assert_eq!(subtitles.follow(None, None, 0.0), Follow::default());
        assert_eq!(
            follow(&mut subtitles, &pgs(), Some(846.0), 1.0).commands,
            Vec::<Command>::new()
        );
    }

    /// The lift of an image track starts from its resting value and moves
    /// by rows, and hiding returns it to the resting value, not to 100.
    #[test]
    fn the_lift_starts_from_the_resting_value() {
        let mut subtitles = Subtitles::default();
        follow(&mut subtitles, &pgs(), None, 0.0);
        assert_eq!(
            follow(&mut subtitles, &pgs(), Some(846.0), 1.0).commands,
            [set(POSITION, json!(92.0))]
        );
        assert_eq!(
            follow(&mut subtitles, &pgs(), None, 0.0).commands,
            [set(POSITION, json!(111.157))]
        );
    }

    /// A step of the fade returns no log line, and the same placement
    /// returns its line once.
    #[test]
    fn a_change_of_placement_or_band_returns_one_log_line() {
        let mut subtitles = Subtitles::default();
        let logged: Vec<Vec<String>> = [
            (pgs(), None, 0.0),
            (pgs(), Some(846.0), 0.5),
            (pgs(), Some(846.0), 1.0),
            (pgs(), None, 0.0),
            (ass(), None, 0.0),
        ]
        .into_iter()
        .map(|(placed, top, fade)| follow(&mut subtitles, &placed, top, fade).logged)
        .collect();
        assert_eq!(
            logged,
            [
                vec![
                    "media-display: subtitle track 3, codec \"hdmv_pgs_subtitle\", ends on the house row, with sub-ass-override scale, stretch-image-subs-to-screen yes, sub-scale 0.7541, and sub-pos 111.157".to_string()
                ],
                vec!["media-display: subtitles move above row 846 of the display, to sub-pos 92".to_string()],
                vec![],
                vec!["media-display: subtitles return to their place, sub-pos 111.157".to_string()],
                vec![
                    "media-display: subtitle track 2, codec \"ass\", ends on the house row, with sub-ass-override force, stretch-image-subs-to-screen no, sub-scale 1, and sub-pos 97.358".to_string()
                ],
            ]
        );
    }

    /// The line names why the track is not on the house row.
    #[test]
    fn an_unplaced_track_logs_its_reason() {
        let cases = [
            (
                Reason::Unmeasured,
                "media-display: subtitle track 4, codec \"dvd_subtitle\", is drawn as the disc placed it, with sub-ass-override scale, stretch-image-subs-to-screen no, sub-scale 1, and sub-pos 100, because the presentation has no image block for the track",
            ),
            (
                Reason::Screen {
                    width: 1280,
                    height: 1024,
                },
                "media-display: subtitle track 4, codec \"dvd_subtitle\", is drawn as the disc placed it, with sub-ass-override scale, stretch-image-subs-to-screen no, sub-scale 1, and sub-pos 100, because the screen is 1280x1024, not 16:9",
            ),
        ];
        for (reason, line) in cases {
            let placed = (
                track(4, "dvd_subtitle"),
                Placement {
                    values: PLAIN,
                    lift: Lift::Share,
                    unplaced: Some(reason),
                },
            );
            assert_eq!(
                follow(&mut Subtitles::default(), &placed, None, 0.0).logged,
                [line]
            );
        }
    }

    /// The header is read again for each ASS track that is selected,
    /// including the track of the same number in the next file, and not
    /// for the same track again.
    #[test]
    fn the_script_is_read_once_for_each_selected_ass_track() {
        let mut subtitles = Subtitles::default();
        let film = Some("/media/a.mkv");
        let next = Some("/media/b.mkv");
        let steps = [
            (film, None, false),
            (film, Some(track(2, "ass")), true),
            (film, Some(track(2, "ass")), false),
            (film, Some(track(1, "subrip")), false),
            (film, Some(track(2, "ass")), true),
            (next, Some(track(2, "ass")), true),
            (next, None, false),
            (next, Some(track(2, "ass")), true),
        ];
        let reads: Vec<bool> = steps
            .iter()
            .map(|(path, track, _)| subtitles.reads_script(*path, track.as_ref()))
            .collect();
        let want: Vec<bool> = steps.iter().map(|(_, _, reads)| *reads).collect();
        assert_eq!(reads, want);
    }
}
