//! Decides how the compositor arranges the windows of the display's claim.
//!
//! A playback pod shows two windows in its claim's region: mpv's, with the
//! app_id `liken-media-video`, and the display's own, with the app_id
//! `liken-media-overlay`. When the compositor offers
//! `liken_arrangement_v1`, the display asks to arrange the claim. The
//! compositor then places, scales, and stacks those surfaces as the
//! display requests, and animates them on each repaint of the output. So
//! the film can shrink into a frame at the credits and return at the
//! panel's refresh rate, while mpv gets no configure and the display draws
//! no frame.
//!
//! This module makes no Wayland calls, so its tests need no connection.
//! Each event and each request returns the arrangement to commit, if any,
//! and the log line to write. `link` sends the arrangements to the
//! compositor and returns its events.
//!
//! When the compositor refuses or offers no arranger, every request
//! returns no change, and the film stays full under the display's corner
//! card.

#[cfg(test)]
mod fakecompositor;
pub mod link;
pub mod protocol;

/// The app_id of mpv's window. `playerArgv` in `player.go` passes the same
/// value to mpv as `--wayland-app-id`.
pub const VIDEO: &str = "liken-media-video";

/// The app_id of the display's own window.
pub const OVERLAY: &str = "liken-media-overlay";

/// How long the film takes to shrink into its frame, and to return. The
/// shrink is slower because it starts on its own at the credits, when
/// nobody is pressing a key. The return follows a key press, so it is
/// quicker.
pub const STEP_BACK_MS: u32 = 900;
pub const RESTORE_MS: u32 = 600;

/// How long the film takes to fade out when the viewer selects the up-next
/// offer. It equals the display's fade-out of the column, so both are
/// clear at the same time.
pub const HAND_OFF_MS: u32 = crate::theme::HAND_OFF.as_millis() as u32;

/// The film's rectangle when it is shrunk, as fractions of the region:
/// left edge, top edge, width, and height. On a 1920 by 1080 region that
/// is 960 by 540 at 96, 270: half the size, on the left margin, centered
/// vertically. The display draws the next work in the right half.
const FILM: [f64; 4] = [0.05, 0.25, 0.5, 0.5];

/// How the compositor fits a surface into its rectangle. The values are
/// the numbers of the protocol's `fit` enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Fit {
    /// Scale the surface and never send its client a configure.
    Scale = 0,
}

/// The curve of an animation. The values are the numbers of the
/// protocol's `curve` enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Curve {
    /// 3t² - 2t³, the curve the brand's motion uses for a move that starts
    /// and ends at rest.
    Smoothstep = 0,
    /// t³, for an exit: it starts slowly and ends fast.
    EaseIn = 2,
}

/// Where the compositor shows every surface of the claim with this app_id.
/// The rectangle is in logical pixels of the region. `delay_ms` is how long
/// after the commit the surfaces start to move.
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

/// One arrangement and the animation that moves every surface to it. The
/// first entry is at the bottom of the stack. An arrangement with no
/// entries is the default, where every surface fills the region.
#[derive(Debug, Clone, PartialEq)]
pub struct Arrangement {
    pub name: &'static str,
    pub entries: Vec<Entry>,
    pub duration_ms: u32,
    pub curve: Curve,
}

/// The film shrunk into a frame on the left, and the display over the
/// whole region so it can draw the next work in the right half. The
/// rectangle is a fixed share of the region, so every region size puts the
/// film in the same place relative to the screen.
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

/// The shrunk film fading out in its frame. Only its opacity changes. The
/// compositor sets the alpha of mpv's window and of every subsurface of
/// it, so the whole frame fades, and whatever is under the claim's
/// surfaces shows through.
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

/// An arrangement to send. The compositor reports `Event::Landed` with
/// the same serial when the animation ends.
#[derive(Debug, Clone, PartialEq)]
pub struct Commit {
    pub serial: u32,
    pub arrangement: Arrangement,
}

/// Why the compositor refused the display's request to arrange. The
/// values are the numbers of the protocol's `refusal` enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refusal {
    /// The display did not connect on a claim's socket.
    NotAClaim,
    /// Another client of the claim arranges it.
    Busy,
    /// A reason that this version of the protocol does not define.
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
            Refusal::NotAClaim => "the display is not on a claim's socket".to_string(),
            Refusal::Busy => "another client arranges the claim".to_string(),
            Refusal::Other(reason) => format!("the compositor sent reason {reason}"),
        }
    }
}

/// A message from the compositor, or a failure of the connection.
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
    /// How many surfaces of the claim have this app_id.
    Surfaces {
        app_id: String,
        count: u32,
    },
    /// The animation of the commit with this serial ended.
    Landed(u32),
    /// The connection failed, with the error the client library returned.
    Lost(String),
}

/// Whether the compositor lets the display arrange the claim.
#[derive(Debug, Clone, PartialEq, Eq)]
enum Status {
    /// The compositor has not replied to the request yet.
    Waiting,
    Granted,
    /// The compositor refused, offers no arranger, or the connection
    /// failed.
    Unavailable,
}

/// The film's arrangement as the display last requested it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FilmState {
    Full,
    /// Shrunk into its frame, or on the way there. `landed` is true after
    /// the compositor reported that the animation ended.
    SteppedBack {
        serial: u32,
        landed: bool,
    },
    /// Shrunk and fading out because the viewer selected the up-next offer.
    /// `landed` is true after the compositor reported that the fade ended.
    HandingOff {
        serial: u32,
        landed: bool,
    },
}

/// The result of one event or request: the arrangement to commit, if any,
/// and the log line that says what changed and why.
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

/// The display's side of the arrangement protocol.
#[derive(Debug, Clone, PartialEq)]
pub struct Arranger {
    status: Status,
    region: Option<(i32, i32)>,
    video_surfaces: u32,
    film: FilmState,
    serial: u32,
    /// The serial of a restore whose `Landed` event has not arrived.
    restoring: Option<u32>,
}

impl Default for Arranger {
    fn default() -> Self {
        Self {
            status: Status::Waiting,
            region: None,
            video_surfaces: 0,
            film: FilmState::Full,
            serial: 0,
            restoring: None,
        }
    }
}

impl Arranger {
    /// Whether the film is shrunk, on its way there, or fading out in its
    /// frame. The display then draws the next work beside the film instead
    /// of the corner card.
    pub fn stepped_back(&self) -> bool {
        matches!(
            self.film,
            FilmState::SteppedBack { .. } | FilmState::HandingOff { .. }
        )
    }

    /// Whether the film has finished shrinking. The display draws no frame
    /// while the film moves, so it draws the column beside the film only
    /// after this is true.
    pub fn landed(&self) -> bool {
        matches!(self.film, FilmState::SteppedBack { landed: true, .. })
    }

    pub fn on_event(&mut self, event: Event) -> Change {
        match event {
            Event::Granted => {
                self.status = Status::Granted;
                Change::line(log_line("granted the claim, so the display arranges its surfaces"))
            }
            Event::Refused(reason) => self.become_unavailable(format!(
                "refused the claim because {}, so the film is never shrunk",
                reason.describe()
            )),
            Event::Absent => self.become_unavailable(
                "is absent because the compositor does not offer liken_arranger_manager_v1, so the film is never shrunk"
                    .to_string(),
            ),
            Event::Lost(error) => self.become_unavailable(format!(
                "lost its connection ({error}), so the compositor returns every surface to its default"
            )),
            Event::Region { width, height } => self.on_region(width, height),
            Event::Surfaces { app_id, count } => {
                if app_id != VIDEO || count == self.video_surfaces {
                    return Change::default();
                }
                self.video_surfaces = count;
                Change::line(log_line(&format!(
                    "counts {count} {VIDEO} surface{} on the claim",
                    if count == 1 { "" } else { "s" }
                )))
            }
            Event::Landed(serial) => match self.film {
                FilmState::SteppedBack {
                    serial: current,
                    landed: false,
                } if current == serial => {
                    self.film = FilmState::SteppedBack {
                        serial,
                        landed: true,
                    };
                    Change::line(log_line(&format!(
                        "finished shrinking the film (serial {serial}), so the display draws the next work beside it"
                    )))
                }
                FilmState::HandingOff {
                    serial: current,
                    landed: false,
                } if current == serial => {
                    self.film = FilmState::HandingOff {
                        serial,
                        landed: true,
                    };
                    Change::line(log_line(&format!(
                        "finished fading out the film (serial {serial}), so the film is at opacity 0"
                    )))
                }
                FilmState::Full if self.restoring == Some(serial) => {
                    self.restoring = None;
                    Change::line(log_line(&format!(
                        "finished restoring the film (serial {serial}), so the film fills the region"
                    )))
                }
                _ => Change::default(),
            },
        }
    }

    /// Records that the compositor refused, offers no arranger, or the
    /// connection failed. The compositor shows every surface at the
    /// default in each case, so the film is full whatever the display
    /// requested before.
    fn become_unavailable(&mut self, why: String) -> Change {
        self.status = Status::Unavailable;
        self.film = FilmState::Full;
        Change::line(log_line(&why))
    }

    /// The compositor keeps rectangles in pixels, so a shrunk film needs
    /// its arrangement committed again at the new size. The commit takes
    /// no time, because the viewer already watched the film move.
    fn on_region(&mut self, width: i32, height: i32) -> Change {
        if self.region == Some((width, height)) {
            return Change::default();
        }
        self.region = Some((width, height));
        let line = log_line(&format!("received region {width}x{height} for the claim"));
        if !self.stepped_back() || width <= 0 || height <= 0 {
            return Change::line(line);
        }
        let fading = matches!(self.film, FilmState::HandingOff { .. });
        let mut arrangement = match fading {
            true => handing_off(width, height),
            false => stepped_back(width, height),
        };
        arrangement.duration_ms = 0;
        let serial = self.next_serial();
        // This commit replaces the commit in flight, and the compositor
        // sends no `Landed` for a replaced commit. A film that had
        // already landed stays landed and keeps its column drawn. A film
        // that was still moving waits for the `Landed` of the new serial,
        // which arrives at once because this commit has no duration.
        let landed = self.landed();
        self.film = match fading {
            true => FilmState::HandingOff {
                serial,
                landed: true,
            },
            false => FilmState::SteppedBack { serial, landed },
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement,
            }),
            line: Some(format!("{line}, so the film is shrunk again at that size")),
        }
    }

    /// Shrinks the film because `cause` happened. It returns no change
    /// without an arranger, before the region's size is known, when no
    /// surface of mpv's is on the claim, and when the film is already
    /// shrunk.
    pub fn step_back(&mut self, cause: &str) -> Change {
        let Some((width, height)) = self.region.filter(|_| self.ready()) else {
            return Change::default();
        };
        if self.stepped_back() || width <= 0 || height <= 0 {
            return Change::default();
        }
        let serial = self.next_serial();
        self.film = FilmState::SteppedBack {
            serial,
            landed: false,
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement: stepped_back(width, height),
            }),
            line: Some(log_line(&format!(
                "started shrinking the film (serial {serial}) over {STEP_BACK_MS} ms, because {cause}"
            ))),
        }
    }

    /// Fades the shrunk film out in its frame because `cause` happened. It
    /// returns no change unless the film is shrunk. A film at the default
    /// plays under the corner card, and the card leaves on its own.
    pub fn hand_off(&mut self, cause: &str) -> Change {
        let Some((width, height)) = self.region.filter(|_| self.stepped_back()) else {
            return Change::default();
        };
        let serial = self.next_serial();
        self.film = FilmState::HandingOff {
            serial,
            landed: false,
        };
        Change {
            commit: Some(Commit {
                serial,
                arrangement: handing_off(width, height),
            }),
            line: Some(log_line(&format!(
                "started fading out the film (serial {serial}) over {HAND_OFF_MS} ms, because {cause}"
            ))),
        }
    }

    /// Returns the film to the whole region because `cause` happened. It
    /// returns no change unless the film is shrunk.
    pub fn restore(&mut self, cause: &str) -> Change {
        if !self.stepped_back() {
            return Change::default();
        }
        let serial = self.next_serial();
        self.film = FilmState::Full;
        self.restoring = Some(serial);
        Change {
            commit: Some(Commit {
                serial,
                arrangement: restored(),
            }),
            line: Some(log_line(&format!(
                "started restoring the film (serial {serial}) over {RESTORE_MS} ms, because {cause}"
            ))),
        }
    }

    /// Whether shrinking the film can move anything: the compositor
    /// granted the claim and mpv's window is on it.
    fn ready(&self) -> bool {
        self.status == Status::Granted && self.video_surfaces > 0
    }

    fn next_serial(&mut self) -> u32 {
        self.serial = self.serial.wrapping_add(1);
        self.serial
    }
}

fn log_line(what: &str) -> String {
    format!("media-display: arranger {what}")
}

#[cfg(test)]
mod tests {
    use super::*;

    /// An arranger that the compositor granted, with a 1920 by 1080 region
    /// and one surface of mpv's.
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

    /// A granted arranger whose film is shrunk and whose animation ended.
    fn shrunk_and_landed() -> Arranger {
        let mut arranger = granted();
        let serial = commit(arranger.step_back("the card rose")).serial;
        arranger.on_event(Event::Landed(serial));
        arranger
    }

    fn commit(change: Change) -> Commit {
        change.commit.expect("the change has a commit")
    }

    #[test]
    fn the_film_plays_at_the_default() {
        let arranger = granted();
        assert!(!arranger.stepped_back());
        assert!(!arranger.landed());
    }

    #[test]
    fn stepping_back_shrinks_the_film_into_the_left_half() {
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
    fn the_step_back_landing_lets_the_display_draw_the_column() {
        let arranger = shrunk_and_landed();
        assert!(arranger.stepped_back());
        assert!(arranger.landed());
    }

    #[test]
    fn a_landed_event_for_another_serial_changes_nothing() {
        let mut arranger = granted();
        let serial = commit(arranger.step_back("the card rose")).serial;
        let change = arranger.on_event(Event::Landed(serial + 7));
        assert_eq!(change, Change::default());
        assert!(!arranger.landed());
    }

    #[test]
    fn a_restore_returns_the_film_to_the_whole_region() {
        let mut arranger = shrunk_and_landed();
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
                "media-display: arranger started restoring the film (serial 2) over 600 ms, because back was pressed"
            )
        );
        assert!(!arranger.stepped_back());
        assert!(!arranger.landed());

        let landed = arranger.on_event(Event::Landed(2));
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger finished restoring the film (serial 2), so the film fills the region"
            )
        );
        assert_eq!(arranger.on_event(Event::Landed(2)), Change::default());
    }

    /// Each commit has its own serial, so a `Landed` event identifies
    /// the commit it reports.
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
        let mut arranger = shrunk_and_landed();
        assert_eq!(arranger.step_back("the card rose"), Change::default());
        assert!(arranger.landed());
    }

    /// `step_back` commits nothing until the compositor has granted the
    /// claim, reported the region, and reported a surface of mpv's.
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

    #[test]
    fn a_lost_connection_returns_the_film_to_full() {
        let mut arranger = shrunk_and_landed();
        arranger.on_event(Event::Lost("Broken pipe".to_string()));
        assert!(!arranger.stepped_back());
        assert_eq!(arranger.restore("back was pressed"), Change::default());
    }

    /// A new region size commits the shrunk film again with no duration, and
    /// the column stays drawn.
    #[test]
    fn a_new_region_commits_the_step_back_again_at_once() {
        let mut arranger = shrunk_and_landed();
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
            Some("media-display: arranger received region 1280x720 for the claim")
        );
    }

    /// Each state change writes one log line with its cause. An event that
    /// changes nothing writes none.
    #[test]
    fn each_change_writes_one_line_with_its_cause() {
        let cases: [(Event, Option<&str>); 7] = [
            (
                Event::Granted,
                Some(
                    "media-display: arranger granted the claim, so the display arranges its surfaces",
                ),
            ),
            (
                Event::Refused(Refusal::Busy),
                Some(
                    "media-display: arranger refused the claim because another client arranges the claim, so the film is never shrunk",
                ),
            ),
            (
                Event::Refused(Refusal::from_wire(0)),
                Some(
                    "media-display: arranger refused the claim because the display is not on a claim's socket, so the film is never shrunk",
                ),
            ),
            (
                Event::Refused(Refusal::from_wire(9)),
                Some(
                    "media-display: arranger refused the claim because the compositor sent reason 9, so the film is never shrunk",
                ),
            ),
            (
                Event::Absent,
                Some(
                    "media-display: arranger is absent because the compositor does not offer liken_arranger_manager_v1, so the film is never shrunk",
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
                Some("media-display: arranger counts 1 liken-media-video surface on the claim"),
            ),
        ];
        for (event, want) in cases {
            let mut arranger = Arranger::default();
            let change = arranger.on_event(event.clone());
            assert_eq!(change.line.as_deref(), want, "{event:?}");
        }
    }

    /// The film keeps its shrunk rectangle and fades to opacity 0 on the
    /// ease-in curve, over the time the display takes to fade the column.
    #[test]
    fn a_hand_off_fades_the_film_out_in_its_frame() {
        let mut arranger = shrunk_and_landed();
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
                "media-display: arranger started fading out the film (serial 2) over 300 ms, because the up-next offer was taken"
            )
        );
        assert!(arranger.stepped_back());
        assert!(!arranger.landed(), "the column never returns");
    }

    #[test]
    fn a_film_at_the_default_has_nothing_to_hand_off() {
        let mut arranger = granted();
        assert_eq!(
            arranger.hand_off("the up-next offer was taken"),
            Change::default()
        );
    }

    #[test]
    fn a_new_region_during_the_hand_off_keeps_the_film_out() {
        let mut arranger = shrunk_and_landed();
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

    /// When the hand-off lands, `landed` stays false, so the display draws
    /// no column.
    #[test]
    fn the_hand_off_landing_leaves_no_column() {
        let mut arranger = shrunk_and_landed();
        let serial = commit(arranger.hand_off("the up-next offer was taken")).serial;
        let landed = arranger.on_event(Event::Landed(serial));
        assert_eq!(landed.commit, None);
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger finished fading out the film (serial 2), so the film is at opacity 0"
            )
        );
        assert!(!arranger.landed());
        assert_eq!(arranger.on_event(Event::Landed(serial)), Change::default());
    }

    #[test]
    fn the_step_back_and_its_landing_log_their_serial_and_cause() {
        let mut arranger = granted();
        let change = arranger.step_back("the up-next card rose at the credits");
        assert_eq!(
            change.line.as_deref(),
            Some(
                "media-display: arranger started shrinking the film (serial 1) over 900 ms, because the up-next card rose at the credits"
            )
        );
        let landed = arranger.on_event(Event::Landed(1));
        assert_eq!(
            landed.line.as_deref(),
            Some(
                "media-display: arranger finished shrinking the film (serial 1), so the display draws the next work beside it"
            )
        );
    }
}
