//! The display as its claim's arranger.
//!
//! A playback pod shows two surfaces on its claim's region: mpv's window,
//! which carries the app_id `liken-media-video`, and the display's own
//! window, which carries `liken-media-overlay`. On a screen whose compositor
//! offers `liken_arrangement_v1`, the display asks to arrange the claim, and
//! the compositor then places, scales, and stacks those surfaces where the
//! display says, and tweens them there on each repaint of the output. So the
//! film can step back into a frame at the credits and return, at the panel's
//! refresh rate, with no configure to mpv and no frame from the display while
//! it moves.
//!
//! This module decides what the display asks for. It holds what the
//! compositor last said and where the film stands, and it answers each event
//! and each request with the arrangement to commit and the line to log. It
//! speaks no Wayland, so its tests open no connection: `link` carries the
//! arrangements to the compositor and the events back.
//!
//! With no arranger, because the compositor refused or offers no such
//! global, every request answers nothing, and the display keeps its corner
//! card over a film that never moves.

#[cfg(test)]
mod fakecompositor;
pub mod link;
pub mod protocol;

/// The app_id of mpv's window. `playerArgv` in `player.go` passes the same
/// value to mpv as `--wayland-app-id`.
pub const VIDEO: &str = "liken-media-video";

/// The app_id of the display's own window.
pub const OVERLAY: &str = "liken-media-overlay";

/// How long the film takes to step back, and to return. The step back is the
/// slower of the two, because it moves on its own at the credits and a
/// person may not be looking. A return answers a press, so it is quicker.
pub const STEP_BACK_MS: u32 = 900;
pub const RESTORE_MS: u32 = 600;

/// How long the film takes to fade out when a select takes the offer.
/// It is the display's own hand-off fade, so the film and the column reach
/// clear together.
pub const HAND_OFF_MS: u32 = crate::theme::HAND_OFF.as_millis() as u32;

/// Where the stepped-back film shows, as fractions of the region: its left
/// edge, its top edge, its width, and its height. On a 1920 by 1080 region
/// that is 960 by 540 at 96, 270: half the size, on the left margin, and
/// centred on the middle row. The next work takes the right half.
const FILM: [f64; 4] = [0.05, 0.25, 0.5, 0.5];

/// How the compositor fits a surface into its rectangle, as the protocol's
/// `fit` enum numbers it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Fit {
    /// Scale the surface and never send its client a configure.
    Scale = 0,
}

/// The curve a tween follows, as the protocol's `curve` enum numbers it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Curve {
    /// 3t² - 2t³, the curve the brand's motion uses for a move that starts
    /// and ends at rest.
    Smoothstep = 0,
    /// t³, for an exit: it starts slowly and ends fast.
    EaseIn = 2,
}

/// One entry of an arrangement: the surfaces with one app_id, their
/// rectangle in logical pixels of the region, their opacity, and their fit.
#[derive(Debug, Clone, PartialEq)]
pub struct Entry {
    pub app_id: &'static str,
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
    pub opacity: f64,
    pub fit: Fit,
    pub delay_ms: u32,
}

/// One arrangement: its entries, first at the bottom of the stack, and the
/// tween that moves every surface there. An arrangement with no entries is
/// the default, where every surface fills the region.
#[derive(Debug, Clone, PartialEq)]
pub struct Arrangement {
    pub name: &'static str,
    pub entries: Vec<Entry>,
    pub duration_ms: u32,
    pub curve: Curve,
}

/// The film in its frame on the left and the display over the whole region,
/// so the display draws the next work in the right half beside the film.
/// The rectangle comes from the region's own size, so a region of another
/// size steps the film back to the same share of it.
pub fn stepped_back(width: i32, height: i32) -> Arrangement {
    let share = |fraction: f64, of: i32| (fraction * f64::from(of)).round() as i32;
    Arrangement {
        name: "stepped-back",
        entries: vec![
            Entry {
                app_id: VIDEO,
                x: share(FILM[0], width),
                y: share(FILM[1], height),
                width: share(FILM[2], width),
                height: share(FILM[3], height),
                opacity: 1.0,
                fit: Fit::Scale,
                delay_ms: 0,
            },
            Entry {
                app_id: OVERLAY,
                x: 0,
                y: 0,
                width,
                height,
                opacity: 1.0,
                fit: Fit::Scale,
                delay_ms: 0,
            },
        ],
        duration_ms: STEP_BACK_MS,
        curve: Curve::Smoothstep,
    }
}

/// The film in its frame, faded out. The film keeps its stepped-back
/// rectangle, so only its opacity moves. The compositor sets the alpha of
/// mpv's window and of every subsurface under it, so the whole frame fades,
/// and what stands under the claim's surfaces shows through it.
pub fn handing_off(width: i32, height: i32) -> Arrangement {
    let mut arrangement = stepped_back(width, height);
    arrangement.name = "handing-off";
    arrangement.entries[0].opacity = 0.0;
    arrangement.duration_ms = HAND_OFF_MS;
    arrangement.curve = Curve::EaseIn;
    arrangement
}

/// The default arrangement, which returns the film to the whole region.
pub fn restored() -> Arrangement {
    Arrangement {
        name: "restored",
        entries: Vec::new(),
        duration_ms: RESTORE_MS,
        curve: Curve::Smoothstep,
    }
}

/// One arrangement to send, with the serial that `landed` names when its
/// tween ends.
#[derive(Debug, Clone, PartialEq)]
pub struct Commit {
    pub serial: u32,
    pub arrangement: Arrangement,
}

/// Why the compositor refused to let the display arrange, as the protocol's
/// `refusal` enum numbers it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refusal {
    /// The display did not arrive on a claim's socket.
    NotAClaim,
    /// Another client of the claim arranges it.
    Busy,
    /// A reason this version of the protocol does not name.
    Other(u32),
}

impl Refusal {
    pub fn from_wire(reason: u32) -> Self {
        match reason {
            0 => Refusal::NotAClaim,
            1 => Refusal::Busy,
            other => Refusal::Other(other),
        }
    }

    fn describe(self) -> String {
        match self {
            Refusal::NotAClaim => "the display is on no claim's socket".to_string(),
            Refusal::Busy => "another client arranges the claim".to_string(),
            Refusal::Other(reason) => format!("reason {reason}"),
        }
    }
}

/// What the compositor said, or what happened to the connection.
#[derive(Debug, Clone, PartialEq)]
pub enum Event {
    /// The compositor offers no `liken_arranger_manager_v1`.
    Absent,
    Granted,
    Refused(Refusal),
    /// The size of the claim's region, in logical pixels.
    Region {
        width: i32,
        height: i32,
    },
    /// How many surfaces of the claim carry one app_id.
    Surfaces {
        app_id: String,
        count: u32,
    },
    /// The commit with this serial reached its end.
    Landed(u32),
    /// The connection failed, with the error the client library gave.
    Lost(String),
}

/// Whether the display arranges the claim.
#[derive(Debug, Clone, PartialEq, Eq)]
enum Status {
    /// The binding has not answered yet.
    Asking,
    Granted,
    /// The compositor refused, offers no arranger, or the connection
    /// failed. Every surface then shows at the default, and the display
    /// keeps its corner card.
    Without,
}

/// Where the film stands.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Pose {
    Full,
    /// Stepped back into its frame. `landed` holds once the compositor
    /// reported the end of the tween that put it there.
    SteppedBack {
        serial: u32,
        landed: bool,
    },
    /// Stepped back and fading out, because a select took the offer.
    /// `landed` holds once the compositor reported the end of the fade.
    HandingOff {
        serial: u32,
        landed: bool,
    },
}

/// What one event or request changed: the arrangement to commit, if any,
/// and the line that says what changed and why.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Change {
    pub commit: Option<Commit>,
    pub line: Option<String>,
}

impl Change {
    fn line(line: String) -> Self {
        Self {
            commit: None,
            line: Some(line),
        }
    }
}

/// The display's side of the arrangement: what the compositor granted, the
/// region's size, whether mpv's window is on the claim, and where the film
/// stands.
#[derive(Debug, Clone, PartialEq)]
pub struct Arranger {
    status: Status,
    region: Option<(i32, i32)>,
    films: u32,
    pose: Pose,
    serial: u32,
    /// The serial of a restore whose landing has not arrived.
    restoring: Option<u32>,
}

impl Default for Arranger {
    fn default() -> Self {
        Self {
            status: Status::Asking,
            region: None,
            films: 0,
            pose: Pose::Full,
            serial: 0,
            restoring: None,
        }
    }
}

impl Arranger {
    /// Whether the film stands stepped back, or is on its way there, or
    /// fades out in its frame. The display then draws the next work
    /// beside the film instead of the corner card.
    pub fn stepped_back(&self) -> bool {
        matches!(
            self.pose,
            Pose::SteppedBack { .. } | Pose::HandingOff { .. }
        )
    }

    /// Whether the film has reached its frame. The display draws no frame
    /// while the film moves, so the column it draws beside the film waits
    /// for this.
    pub fn landed(&self) -> bool {
        matches!(self.pose, Pose::SteppedBack { landed: true, .. })
    }

    /// Take one event from the compositor.
    pub fn on_event(&mut self, event: Event) -> Change {
        match event {
            Event::Granted => {
                self.status = Status::Granted;
                Change::line(say("granted, so the display arranges the claim's surfaces"))
            }
            Event::Refused(reason) => self.without(format!(
                "refused, because {}, so the film never steps back",
                reason.describe()
            )),
            Event::Absent => self.without(
                "absent, because the compositor offers no liken_arranger_manager_v1, so the film never steps back"
                    .to_string(),
            ),
            Event::Lost(error) => self.without(format!(
                "lost, because the connection failed ({error}), so the compositor returns every surface to its default"
            )),
            Event::Region { width, height } => self.on_region(width, height),
            Event::Surfaces { app_id, count } => {
                if app_id != VIDEO || count == self.films {
                    return Change::default();
                }
                self.films = count;
                Change::line(say(&format!(
                    "the claim holds {count} {VIDEO} surface{}",
                    if count == 1 { "" } else { "s" }
                )))
            }
            Event::Landed(serial) => match self.pose {
                Pose::SteppedBack {
                    serial: standing,
                    landed: false,
                } if standing == serial => {
                    self.pose = Pose::SteppedBack {
                        serial,
                        landed: true,
                    };
                    Change::line(say(&format!(
                        "stepped-back landed (serial {serial}), so the display draws the next work beside the film"
                    )))
                }
                Pose::HandingOff {
                    serial: standing,
                    landed: false,
                } if standing == serial => {
                    self.pose = Pose::HandingOff {
                        serial,
                        landed: true,
                    };
                    Change::line(say(&format!(
                        "handing-off landed (serial {serial}), so the film stands at opacity 0"
                    )))
                }
                Pose::Full if self.restoring == Some(serial) => {
                    self.restoring = None;
                    Change::line(say(&format!(
                        "restored landed (serial {serial}), so the film fills the region"
                    )))
                }
                _ => Change::default(),
            },
        }
    }

    /// The compositor refused, has no arranger, or went away. Every surface
    /// shows at the default, so the film is full again whatever the display
    /// asked for before.
    fn without(&mut self, why: String) -> Change {
        self.status = Status::Without;
        self.pose = Pose::Full;
        Change::line(say(&why))
    }

    /// The region took a new size. The compositor keeps the rectangles in
    /// pixels, so a film standing back is committed again at the new size,
    /// at once, because the person already watched it move.
    fn on_region(&mut self, width: i32, height: i32) -> Change {
        if self.region == Some((width, height)) {
            return Change::default();
        }
        self.region = Some((width, height));
        let line = say(&format!("the claim's region is {width}x{height}"));
        if !self.stepped_back() || width <= 0 || height <= 0 {
            return Change::line(line);
        }
        let fading = matches!(self.pose, Pose::HandingOff { .. });
        let mut arrangement = match fading {
            true => handing_off(width, height),
            false => stepped_back(width, height),
        };
        arrangement.duration_ms = 0;
        let serial = self.next_serial();
        // The arrangement lands at once and the column already stands, so
        // the pose keeps its landing under the new serial. A film handing
        // off stays at opacity 0 at the new size.
        let landed = self.landed();
        self.pose = match fading {
            true => Pose::HandingOff {
                serial,
                landed: true,
            },
            false => Pose::SteppedBack { serial, landed },
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement,
            }),
            line: Some(format!("{line}, so the film steps back at that size")),
        }
    }

    /// Step the film back, because `cause` happened. It answers nothing
    /// without an arranger, before the region's size is known, with no
    /// surface of mpv's on the claim, or with the film already back.
    pub fn step_back(&mut self, cause: &str) -> Change {
        let Some((width, height)) = self.region.filter(|_| self.ready()) else {
            return Change::default();
        };
        if self.stepped_back() || width <= 0 || height <= 0 {
            return Change::default();
        }
        let serial = self.next_serial();
        self.pose = Pose::SteppedBack {
            serial,
            landed: false,
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement: stepped_back(width, height),
            }),
            line: Some(say(&format!(
                "stepped-back (serial {serial}) over {STEP_BACK_MS} ms, because {cause}"
            ))),
        }
    }

    /// Fade the film out where it stands, because `cause` happened. It
    /// answers nothing unless the film stands back, because a film at the
    /// default plays under the corner card, and the card leaves alone.
    pub fn hand_off(&mut self, cause: &str) -> Change {
        let Some((width, height)) = self.region.filter(|_| self.stepped_back()) else {
            return Change::default();
        };
        let serial = self.next_serial();
        self.pose = Pose::HandingOff {
            serial,
            landed: false,
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement: handing_off(width, height),
            }),
            line: Some(say(&format!(
                "handing-off (serial {serial}) over {HAND_OFF_MS} ms, because {cause}"
            ))),
        }
    }

    /// Return the film to the whole region, because `cause` happened. It
    /// answers nothing unless the film stands back.
    pub fn restore(&mut self, cause: &str) -> Change {
        if !self.stepped_back() {
            return Change::default();
        }
        let serial = self.next_serial();
        self.pose = Pose::Full;
        self.restoring = Some(serial);
        Change {
            commit: Some(Commit {
                serial,
                arrangement: restored(),
            }),
            line: Some(say(&format!(
                "restored (serial {serial}) over {RESTORE_MS} ms, because {cause}"
            ))),
        }
    }

    /// Whether a step back can move anything: the compositor granted the
    /// claim, and mpv's window is on it.
    fn ready(&self) -> bool {
        self.status == Status::Granted && self.films > 0
    }

    fn next_serial(&mut self) -> u32 {
        self.serial = self.serial.wrapping_add(1);
        self.serial
    }
}

/// One line of the display's record about the arranger.
fn say(what: &str) -> String {
    format!("media-display: arranger {what}")
}

#[cfg(test)]
mod tests {
    use super::*;

    /// An arranger the compositor granted, on a 1920 by 1080 region that
    /// shows mpv's window.
    fn granted() -> Arranger {
        let mut arranger = Arranger::default();
        for event in [
            Event::Granted,
            Event::Region {
                width: 1920,
                height: 1080,
            },
            Event::Surfaces {
                app_id: VIDEO.to_string(),
                count: 1,
            },
        ] {
            arranger.on_event(event);
        }
        arranger
    }

    /// The arranger with the film stepped back and landed.
    fn standing_back() -> Arranger {
        let mut arranger = granted();
        let serial = commit(arranger.step_back("the card rose")).serial;
        arranger.on_event(Event::Landed(serial));
        arranger
    }

    fn commit(change: Change) -> Commit {
        change.commit.expect("the change commits an arrangement")
    }

    #[test]
    fn the_film_plays_at_the_default() {
        let arranger = granted();
        assert!(!arranger.stepped_back());
        assert!(!arranger.landed());
    }

    #[test]
    fn the_rise_steps_the_film_back_into_the_left_half() {
        let mut arranger = granted();
        let commit = commit(arranger.step_back("the card rose"));
        assert_eq!(
            commit.arrangement,
            Arrangement {
                name: "stepped-back",
                entries: vec![
                    Entry {
                        app_id: "liken-media-video",
                        x: 96,
                        y: 270,
                        width: 960,
                        height: 540,
                        opacity: 1.0,
                        fit: Fit::Scale,
                        delay_ms: 0,
                    },
                    Entry {
                        app_id: "liken-media-overlay",
                        x: 0,
                        y: 0,
                        width: 1920,
                        height: 1080,
                        opacity: 1.0,
                        fit: Fit::Scale,
                        delay_ms: 0,
                    },
                ],
                duration_ms: 900,
                curve: Curve::Smoothstep,
            }
        );
        assert!(arranger.stepped_back());
        assert!(!arranger.landed());
    }

    /// The rectangle is a share of the region, so a 4K region and a 720p one
    /// put the film in the same place on the screen.
    #[test]
    fn the_film_takes_the_same_share_of_any_region() {
        let cases = [
            ((3840, 2160), (192, 540, 1920, 1080)),
            ((1280, 720), (64, 180, 640, 360)),
            ((2560, 1080), (128, 270, 1280, 540)),
        ];
        for ((width, height), want) in cases {
            let film = &stepped_back(width, height).entries[0];
            assert_eq!(
                (film.x, film.y, film.width, film.height),
                want,
                "{width}x{height}"
            );
        }
    }

    #[test]
    fn the_landing_of_the_step_back_shows_the_column() {
        let arranger = standing_back();
        assert!(arranger.stepped_back());
        assert!(arranger.landed());
    }

    /// A landing for a commit the display replaced lands nothing.
    #[test]
    fn a_landing_for_another_serial_lands_nothing() {
        let mut arranger = granted();
        let serial = commit(arranger.step_back("the card rose")).serial;
        let change = arranger.on_event(Event::Landed(serial + 7));
        assert_eq!(change, Change::default());
        assert!(!arranger.landed());
    }

    #[test]
    fn a_restore_returns_the_film_to_the_whole_region() {
        let mut arranger = standing_back();
        let change = arranger.restore("back was pressed");
        assert_eq!(
            commit(change.clone()).arrangement,
            Arrangement {
                name: "restored",
                entries: Vec::new(),
                duration_ms: 600,
                curve: Curve::Smoothstep,
            }
        );
        assert_eq!(
            change.line.as_deref(),
            Some(
                "media-display: arranger restored (serial 2) over 600 ms, because back was pressed"
            )
        );
        assert!(!arranger.stepped_back());
        assert!(!arranger.landed());

        let landed = arranger.on_event(Event::Landed(2));
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger restored landed (serial 2), so the film fills the region"
            )
        );
        assert_eq!(arranger.on_event(Event::Landed(2)), Change::default());
    }

    /// Each commit carries a serial of its own, so a landing names one.
    #[test]
    fn every_commit_takes_a_new_serial() {
        let mut arranger = granted();
        let serials = [
            commit(arranger.step_back("the card rose")).serial,
            commit(arranger.restore("back was pressed")).serial,
        ];
        assert_eq!(serials, [1, 2]);
    }

    #[test]
    fn a_film_at_the_default_has_nothing_to_restore() {
        let mut arranger = granted();
        assert_eq!(arranger.restore("back was pressed"), Change::default());
    }

    #[test]
    fn a_film_already_back_does_not_step_back_again() {
        let mut arranger = standing_back();
        assert_eq!(arranger.step_back("the card rose"), Change::default());
        assert!(arranger.landed());
    }

    /// With no arranger, the display keeps its corner card: the step back
    /// commits nothing and the film never leaves the whole region.
    #[test]
    fn no_arranger_steps_nothing_back() {
        let video = || Event::Surfaces {
            app_id: VIDEO.to_string(),
            count: 1,
        };
        let region = || Event::Region {
            width: 1920,
            height: 1080,
        };
        let cases = [
            ("before an answer", vec![region(), video()]),
            (
                "refused as busy",
                vec![Event::Refused(Refusal::Busy), region(), video()],
            ),
            (
                "refused off a claim",
                vec![Event::Refused(Refusal::NotAClaim)],
            ),
            ("an old compositor", vec![Event::Absent]),
            ("granted with no region", vec![Event::Granted, video()]),
            ("granted with no film", vec![Event::Granted, region()]),
            (
                "granted and then lost",
                vec![
                    Event::Granted,
                    region(),
                    video(),
                    Event::Lost("Broken pipe".to_string()),
                ],
            ),
            (
                "the film's window gone",
                vec![
                    Event::Granted,
                    region(),
                    video(),
                    Event::Surfaces {
                        app_id: VIDEO.to_string(),
                        count: 0,
                    },
                ],
            ),
        ];
        for (name, events) in cases {
            let mut arranger = Arranger::default();
            for event in events {
                arranger.on_event(event);
            }
            assert_eq!(
                arranger.step_back("the card rose"),
                Change::default(),
                "{name}"
            );
            assert!(!arranger.stepped_back(), "{name}");
        }
    }

    /// A failed connection or a refusal while the film stands back leaves
    /// the compositor to return every surface, so the display's own idea of
    /// the film follows.
    #[test]
    fn a_lost_connection_returns_the_film_to_full() {
        let mut arranger = standing_back();
        arranger.on_event(Event::Lost("Broken pipe".to_string()));
        assert!(!arranger.stepped_back());
        assert_eq!(arranger.restore("back was pressed"), Change::default());
    }

    /// A region of a new size moves a film that stands back to the same
    /// share of the new region, at once, and the column stays up.
    #[test]
    fn a_new_region_commits_the_step_back_again_at_once() {
        let mut arranger = standing_back();
        let change = arranger.on_event(Event::Region {
            width: 1280,
            height: 720,
        });
        let commit = commit(change);
        assert_eq!(commit.arrangement.duration_ms, 0);
        assert_eq!(commit.arrangement.entries[0].width, 640);
        assert!(arranger.landed());

        let again = arranger.on_event(Event::Region {
            width: 1280,
            height: 720,
        });
        assert_eq!(again, Change::default());
    }

    #[test]
    fn a_new_region_at_the_default_commits_nothing() {
        let mut arranger = granted();
        let change = arranger.on_event(Event::Region {
            width: 1280,
            height: 720,
        });
        assert_eq!(change.commit, None);
        assert_eq!(
            change.line.as_deref(),
            Some("media-display: arranger the claim's region is 1280x720")
        );
    }

    /// One line for each change of state, with its cause, and none for an
    /// event that changes nothing.
    #[test]
    fn each_change_writes_one_line_with_its_cause() {
        let cases: [(Event, Option<&str>); 7] = [
            (
                Event::Granted,
                Some(
                    "media-display: arranger granted, so the display arranges the claim's surfaces",
                ),
            ),
            (
                Event::Refused(Refusal::Busy),
                Some(
                    "media-display: arranger refused, because another client arranges the claim, so the film never steps back",
                ),
            ),
            (
                Event::Refused(Refusal::from_wire(0)),
                Some(
                    "media-display: arranger refused, because the display is on no claim's socket, so the film never steps back",
                ),
            ),
            (
                Event::Refused(Refusal::from_wire(9)),
                Some(
                    "media-display: arranger refused, because reason 9, so the film never steps back",
                ),
            ),
            (
                Event::Absent,
                Some(
                    "media-display: arranger absent, because the compositor offers no liken_arranger_manager_v1, so the film never steps back",
                ),
            ),
            (
                Event::Surfaces {
                    app_id: OVERLAY.to_string(),
                    count: 1,
                },
                None,
            ),
            (
                Event::Surfaces {
                    app_id: VIDEO.to_string(),
                    count: 1,
                },
                Some("media-display: arranger the claim holds 1 liken-media-video surface"),
            ),
        ];
        for (event, want) in cases {
            let mut arranger = Arranger::default();
            let change = arranger.on_event(event.clone());
            assert_eq!(change.line.as_deref(), want, "{event:?}");
        }
    }

    /// A taken offer fades the film out inside its frame: the film keeps
    /// its stepped-back rectangle and falls to opacity 0 on the ease-in
    /// curve, over the time the display fades the column.
    #[test]
    fn a_hand_off_fades_the_film_out_where_it_stands() {
        let mut arranger = standing_back();
        let change = arranger.hand_off("the up-next offer was taken");
        let commit = commit(change.clone());
        let mut want = stepped_back(1920, 1080);
        want.name = "handing-off";
        want.entries[0].opacity = 0.0;
        want.duration_ms = 300;
        want.curve = Curve::EaseIn;
        assert_eq!(commit.arrangement, want);
        assert_eq!(commit.serial, 2);
        assert_eq!(
            change.line.as_deref(),
            Some(
                "media-display: arranger handing-off (serial 2) over 300 ms, because the up-next offer was taken"
            )
        );
        assert!(arranger.stepped_back());
        assert!(!arranger.landed(), "the column never returns");
    }

    /// A film at the default has no frame to fade in, so the corner card
    /// leaves alone.
    #[test]
    fn a_film_at_the_default_hands_nothing_off() {
        let mut arranger = granted();
        assert_eq!(
            arranger.hand_off("the up-next offer was taken"),
            Change::default()
        );
    }

    /// A region of a new size during the hand-off keeps the film at opacity
    /// 0 at the new size, and never brings it back.
    #[test]
    fn a_new_region_during_the_hand_off_keeps_the_film_out() {
        let mut arranger = standing_back();
        arranger.hand_off("the up-next offer was taken");
        let commit = commit(arranger.on_event(Event::Region {
            width: 1280,
            height: 720,
        }));
        assert_eq!(commit.arrangement.name, "handing-off");
        assert_eq!(commit.arrangement.entries[0].opacity, 0.0);
        assert_eq!(commit.arrangement.entries[0].width, 640);
        assert_eq!(commit.arrangement.duration_ms, 0);
    }

    /// The landing of the hand-off says so, and shows no column.
    #[test]
    fn the_landing_of_the_hand_off_shows_no_column() {
        let mut arranger = standing_back();
        let serial = commit(arranger.hand_off("the up-next offer was taken")).serial;
        let landed = arranger.on_event(Event::Landed(serial));
        assert_eq!(landed.commit, None);
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger handing-off landed (serial 2), so the film stands at opacity 0"
            )
        );
        assert!(!arranger.landed());
        assert_eq!(arranger.on_event(Event::Landed(serial)), Change::default());
    }

    #[test]
    fn the_step_back_and_its_landing_name_their_serial_and_cause() {
        let mut arranger = granted();
        let change = arranger.step_back("the up-next card rose at the credits");
        assert_eq!(
            change.line.as_deref(),
            Some(
                "media-display: arranger stepped-back (serial 1) over 900 ms, because the up-next card rose at the credits"
            )
        );
        let landed = arranger.on_event(Event::Landed(1));
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger stepped-back landed (serial 1), so the display draws the next work beside the film"
            )
        );
    }
}
