//! Routes each remote press to the module that owns the focused stop, and
//! tracks whether the on-screen display (OSD) is shown, which stop has
//! focus, and whether mpv is paused. It draws nothing.

use serde_json::json;

use crate::cast::Cast;
use crate::fade::{Clock, Fade, Hide};
use crate::film::Film;
use crate::images;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::scrubber::{self, Axis, Scrubber};
use crate::skip::Skip;
use crate::strip::Strip;
use crate::upnext::UpNext;

/// A remote press, as the command sidecar names it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Action {
    Up,
    Down,
    Left,
    Right,
    Select,
    Back,
}

impl Action {
    pub fn from_word(word: &str) -> Option<Self> {
        match word {
            "up" => Some(Action::Up),
            "down" => Some(Action::Down),
            "left" => Some(Action::Left),
            "right" => Some(Action::Right),
            "select" => Some(Action::Select),
            "back" => Some(Action::Back),
            _ => None,
        }
    }
}

/// The places on the OSD that can take focus, top to bottom. `Fine` and
/// `Chapter` are the seek and chapter axes of the scrubber bar. Up and down
/// move between the stops that the current file has.
///
/// The cast row is at the top because a viewer reads it while the film is
/// paused. The up-next offer and the skip control are just above the bar,
/// because a viewer uses them while the film plays.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Stop {
    Cast,
    Next,
    Skip,
    Fine,
    Chapter,
    Images,
    Strip,
}

const STOPS: [Stop; 7] = [
    Stop::Cast,
    Stop::Next,
    Stop::Skip,
    Stop::Fine,
    Stop::Chapter,
    Stop::Images,
    Stop::Strip,
];

/// The script-message that asks the command sidecar to end the run. The
/// sidecar publishes the end of the run to the bus, and then quits mpv with
/// exit code 0, so the pod ends as `Completed` and not `Error`. The display
/// does not quit mpv itself, because the end of the run must reach the bus
/// while mpv still draws the film.
const EXIT: &str = "liken-exit";

/// The script-message that taking the up-next offer sends. The command
/// sidecar publishes `play-next` with the offer's request on the Player's
/// commands topic, and the program that created the Play creates the next
/// one.
const NEXT: &str = "liken-next";

/// The modules that a press can reach, and the film state they read.
pub struct Parts<'a> {
    pub presentation: &'a Presentation,
    pub film: &'a Film,
    pub scrubber: &'a mut Scrubber,
    pub strip: &'a mut Strip,
    /// A monotonic time in seconds, for the scrubber's seek acceleration.
    pub now: f64,
    pub upnext: &'a mut UpNext,
    pub skip: &'a mut Skip,
    pub cast: &'a mut Cast,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Focus {
    summoned: bool,
    focused: Option<Stop>,
    paused: bool,
    first_pause: bool,
    clock: Clock,
}

impl Default for Focus {
    fn default() -> Self {
        Self::new()
    }
}

impl Focus {
    /// The OSD starts hidden. `first_pause` makes `on_pause` record mpv's
    /// first pause report without showing the OSD.
    pub fn new() -> Self {
        Self {
            summoned: false,
            focused: None,
            paused: false,
            first_pause: true,
            clock: Clock::default(),
        }
    }

    pub fn visible(self) -> bool {
        self.summoned
    }

    /// Whether a select while the OSD is hidden takes the up-next card. It
    /// does only when the card is shown and no skip control is, and the card
    /// then draws focused so the viewer sees what select will do. When a
    /// skip control is also shown, the card draws unfocused and never takes
    /// the select, because a seek back undoes an unwanted skip, but nothing
    /// undoes a taken offer, which starts the next Play.
    pub fn card_takes_select(self, upnext: &UpNext, skip: &Skip) -> bool {
        upnext.risen_outside(self.summoned) && !skip.available()
    }

    pub fn focused_stop(self) -> Option<Stop> {
        self.focused
    }

    /// The OSD's fade. The frame loop draws the OSD while the fade is above
    /// 0.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    pub fn paused(self) -> bool {
        self.paused
    }

    /// Returns and resets the pending change to the OSD's hide timer. The
    /// frame loop owns the timer and applies the change.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    pub fn axis(self) -> Option<Axis> {
        match self.focused {
            Some(Stop::Fine) => Some(Axis::Fine),
            Some(Stop::Chapter) => Some(Axis::Chapter),
            _ => None,
        }
    }

    /// The stops that have something to show for the current file, top to
    /// bottom. Only these can take focus.
    pub fn present(parts: &Parts<'_>) -> Vec<Stop> {
        STOPS
            .into_iter()
            .filter(|stop| Self::stop_available(*stop, parts))
            .collect()
    }

    fn stop_available(stop: Stop, parts: &Parts<'_>) -> bool {
        let film = parts.film;
        match stop {
            Stop::Cast => parts.cast.available(film),
            Stop::Next => parts.upnext.available(),
            Stop::Skip => parts.skip.available(),
            Stop::Fine => !parts.presentation.is_image() && scrubber::fine_available(film),
            Stop::Chapter => !parts.presentation.is_image() && scrubber::chapter_available(film),
            Stop::Images => images::available(parts.presentation),
            Stop::Strip => Strip::available(parts.presentation, film),
        }
    }

    /// Shows the OSD and arms its hide timer, unless the film is paused or a
    /// chooser is open. When the OSD was hidden, focus goes to the first
    /// stop below the cast row and the up-next offer: the skip control when
    /// it is shown, and otherwise the bar. Focus never starts on the offer,
    /// because a viewer who presses select to pause would take the offer
    /// and start the next Play. Starting on the skip control is safe,
    /// because a seek back undoes a skip.
    pub fn summon(&mut self, parts: &Parts<'_>) {
        self.show(parts, &[Stop::Cast, Stop::Next]);
    }

    /// Shows the OSD. When it was hidden, focus goes to the first present
    /// stop that is not in `skipped`.
    fn show(&mut self, parts: &Parts<'_>, skipped: &[Stop]) {
        if !self.summoned {
            self.summoned = true;
            self.focused = Self::first_present(parts, skipped);
        }
        self.clock.show(self.idle_window(parts));
    }

    /// Whether a chooser or an adjuster of the strip is open and receives
    /// every press.
    fn chooser_open(parts: &Parts<'_>) -> bool {
        parts.strip.capturing().is_some()
    }

    /// The hide-timer change for a shown OSD. The timer is cancelled while
    /// the film is paused, and while a chooser or an adjuster is open so
    /// that the OSD does not hide a list the viewer is reading. Otherwise
    /// it is armed.
    fn idle_window(self, parts: &Parts<'_>) -> Hide {
        match self.paused || Self::chooser_open(parts) {
            true => Hide::Cancel,
            false => Hide::Arm,
        }
    }

    /// Hides the OSD and cancels its hide timer. It also collapses an
    /// expanded up-next card and closes an open chooser, so the next summon
    /// shows the strip and the offer as they first appear.
    pub fn dismiss(&mut self, parts: &mut Parts<'_>) {
        self.summoned = false;
        parts.upnext.collapse();
        parts.strip.close();
        self.clock.request_hide(Hide::Cancel);
        self.clock.hide();
    }

    fn first_present(parts: &Parts<'_>, skipped: &[Stop]) -> Option<Stop> {
        Self::present(parts)
            .into_iter()
            .find(|stop| !skipped.contains(stop))
    }

    /// Moves focus off a stop that is no longer present, such as the skip
    /// control after the playhead leaves its span or a select takes it.
    /// Focus moves to the first present stop below the skip control.
    pub fn refocus(&mut self, parts: &Parts<'_>) {
        let present = Self::present(parts);
        if self.focused.is_some_and(|stop| !present.contains(&stop)) {
            self.focused = Self::first_present(parts, &[Stop::Cast, Stop::Next, Stop::Skip]);
        }
    }

    /// Records a change of mpv's `pause` property. A pause shows the OSD and
    /// keeps it shown, and a resume hides it at once.
    ///
    /// mpv reports the property once when the display starts to observe it.
    /// That report only records the state, so a film that loads paused
    /// starts with the OSD hidden. When a pause shows the OSD, focus skips
    /// the skip control as well as the offer, so the next select resumes the
    /// film.
    pub fn on_pause(&mut self, paused: bool, parts: &mut Parts<'_>) {
        self.paused = paused;
        if self.first_pause {
            self.first_pause = false;
            return;
        }
        // Taking the offer pauses mpv. The OSD stays hidden for that pause,
        // because the next Play is about to replace the screen.
        if paused && parts.upnext.waiting() {
            return;
        }
        if paused {
            self.show(parts, &[Stop::Cast, Stop::Next, Stop::Skip]);
            self.clock.request_hide(Hide::Cancel);
        } else if self.summoned {
            self.dismiss(parts);
        }
    }

    fn step(&mut self, direction: i64, parts: &mut Parts<'_>) {
        let present = Self::present(parts);
        if present.is_empty() {
            return;
        }
        let at = present
            .iter()
            .position(|stop| Some(*stop) == self.focused)
            .unwrap_or(0) as i64;
        let next = present[(at + direction).clamp(0, present.len() as i64 - 1) as usize];
        // A scan in progress would keep its preview on the screen after
        // focus leaves the bar.
        if self.focused == Some(Stop::Fine) && next != Stop::Fine {
            parts.scrubber.cancel();
        }
        if self.focused == Some(Stop::Next) && next != Stop::Next {
            parts.upnext.collapse();
        }
        self.focused = Some(next);
    }

    /// Sends a left, right, or select press to the focused stop.
    fn route(&mut self, action: Action, parts: &mut Parts<'_>) -> Vec<Command> {
        match self.focused {
            // The offer and the skip control react only to select, which
            // `select` handles.
            Some(Stop::Next | Stop::Skip) | None => Vec::new(),
            // `select` does not route a select here, so a select on the cast
            // row plays or pauses.
            Some(Stop::Cast) => {
                match action {
                    Action::Left => parts.cast.step(-1, parts.film),
                    Action::Right => parts.cast.step(1, parts.film),
                    _ => {}
                }
                Vec::new()
            }
            Some(Stop::Fine) => {
                match action {
                    Action::Left => parts.scrubber.seek(-1.0, parts.now, parts.film),
                    Action::Right => parts.scrubber.seek(1.0, parts.now, parts.film),
                    Action::Select => return parts.scrubber.commit(),
                    _ => {}
                }
                Vec::new()
            }
            Some(Stop::Chapter) => match action {
                Action::Left => scrubber::chapter_step(-1, parts.film),
                Action::Right => scrubber::chapter_step(1, parts.film),
                _ => Vec::new(),
            },
            Some(Stop::Images) => images::press(action),
            Some(Stop::Strip) => parts.strip.press(action, parts.presentation, parts.film),
        }
    }

    /// Handles select. It acts on an open chooser, the focused stop, or a
    /// control that draws focused over the hidden OSD, and otherwise toggles
    /// pause.
    fn select(&mut self, parts: &mut Parts<'_>) -> Vec<Command> {
        if parts.strip.capturing().is_some() {
            return parts.strip.handle(Action::Select, parts.film);
        }
        if self.summoned {
            if self.focused == Some(Stop::Next) {
                // The offer draws as a small chip, and the first select
                // expands it into the card, so the viewer sees what it
                // starts. A select on the card takes the offer. A card that
                // appeared on its own near the end of the film takes the
                // first select.
                if parts.upnext.showing_card() {
                    return self.take_offer(parts);
                }
                parts.upnext.expand();
                return Vec::new();
            }
            if self.focused == Some(Stop::Skip) && parts.skip.available() {
                let commands = parts.skip.take();
                self.refocus(parts);
                return commands;
            }
            if self.focused == Some(Stop::Strip) {
                return self.route(Action::Select, parts);
            }
            if self.focused == Some(Stop::Fine) && parts.scrubber.scanning() {
                return parts.scrubber.commit();
            }
        }
        // While the OSD is hidden, the skip control for an intro or a recap
        // draws focused, so select takes it and the OSD stays hidden.
        if !self.summoned && parts.skip.takes_select_outside() {
            return parts.skip.take();
        }
        if self.card_takes_select(parts.upnext, parts.skip) {
            return self.take_offer(parts);
        }
        // `on_pause` shows the OSD when mpv reports the pause, so this needs
        // no summon. The `no-osd` prefix keeps mpv from drawing its own
        // pause message.
        vec![vec![json!("no-osd"), json!("cycle"), json!("pause")]]
    }

    /// Takes the up-next offer: the card fades out, the command sidecar is
    /// asked for the next Play, and mpv pauses, so the credits' sound stops
    /// at the press and does not play on while the next Play starts.
    fn take_offer(&self, parts: &mut Parts<'_>) -> Vec<Command> {
        parts.upnext.take(self.clock.fade().value());
        vec![next(), set_pause()]
    }

    /// Handles one press and returns the mpv commands it produces.
    ///
    /// A press that opens a chooser or an adjuster cancels the hide timer,
    /// and the press that closes it arms the timer again, so the OSD hides a
    /// full timeout after the chooser closes.
    pub fn nav(&mut self, action: Action, parts: &mut Parts<'_>) -> Vec<Command> {
        let chooser_was_open = Self::chooser_open(parts);
        let commands = self.handle_press(action, parts);
        if Self::chooser_open(parts) {
            self.clock.request_hide(Hide::Cancel);
        } else if chooser_was_open && self.summoned {
            self.clock.request_hide(self.idle_window(parts));
        }
        commands
    }

    /// Back does the first of these that applies: it closes an open
    /// chooser, cancels a scan, hides the OSD, or returns a film that was
    /// shrunk beside the up-next offer to the whole screen. When none
    /// applies, back asks the sidecar to end the run.
    fn handle_press(&mut self, action: Action, parts: &mut Parts<'_>) -> Vec<Command> {
        // After the offer is taken, every press except back is ignored
        // while the next Play starts. Back still ends the run, so the viewer
        // can always leave.
        if parts.upnext.waiting() {
            if action == Action::Back {
                return vec![exit()];
            }
            return Vec::new();
        }

        if action == Action::Back {
            if parts.strip.capturing().is_some() {
                parts.strip.close();
            } else if self.focused == Some(Stop::Fine) && parts.scrubber.scanning() {
                // The film keeps playing from where it was.
                parts.scrubber.cancel();
            } else if self.summoned {
                self.dismiss(parts);
            } else if parts.upnext.stepped_back() {
                parts.upnext.restore();
            } else {
                return vec![exit()];
            }
            return Vec::new();
        }

        if action == Action::Select {
            return self.select(parts);
        }

        // A direction press on a hidden OSD only shows it, so the viewer
        // sees where focus is before a press changes anything.
        let was_visible = self.summoned;
        self.summon(parts);
        if !was_visible {
            return Vec::new();
        }

        if parts.strip.capturing().is_some() {
            return parts.strip.handle(action, parts.film);
        }

        match action {
            Action::Up => self.step(-1, parts),
            Action::Down => self.step(1, parts),
            _ => return self.route(action, parts),
        }
        Vec::new()
    }
}

fn exit() -> Command {
    vec![json!("script-message"), json!(EXIT)]
}

fn next() -> Command {
    vec![json!("script-message"), json!(NEXT)]
}

/// Sets `pause` instead of toggling it, so a film that is already paused
/// stays paused.
fn set_pause() -> Command {
    vec![json!("no-osd"), json!("set"), json!("pause"), json!("yes")]
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::marks::{Jump, Kind, Span};
    use crate::strip::Control;
    use serde_json::json;

    /// A film and every module that a press can reach.
    struct Display {
        presentation: Presentation,
        film: Film,
        scrubber: Scrubber,
        strip: Strip,
        focus: Focus,
        now: f64,
        upnext: UpNext,
        skip: Skip,
        cast: Cast,
    }

    impl Display {
        fn new(block: &str) -> Self {
            let mut presentation = Presentation::default();
            presentation.receive(block);
            let mut film = Film::default();
            film.apply("duration", &json!(6000.0));
            film.apply("time-pos", &json!(1200.0));
            film.apply(
                "chapter-list",
                &json!([{ "time": 0.0 }, { "time": 1500.0 }, { "time": 4500.0 }]),
            );
            film.apply("chapter", &json!(1));
            film.apply(
                "track-list",
                &json!([
                    { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                    { "id": 2, "type": "audio", "lang": "fra" },
                    { "id": 1, "type": "sub", "lang": "spa" },
                ]),
            );
            Self {
                presentation,
                film,
                scrubber: Scrubber::default(),
                strip: Strip::default(),
                focus: Focus::new(),
                now: 100.0,
                upnext: UpNext::default(),
                skip: Skip::default(),
                cast: Cast::default(),
            }
        }

        /// Puts the playhead at the start of the intro, so the skip control
        /// shows over the bare video.
        fn intro(&mut self) {
            self.skip.on_position(Some(INTRO.span.start), Some(INTRO));
        }

        /// Puts the playhead 100 seconds into the intro, past the 15 seconds
        /// that the skip control shows over the bare video.
        fn late_intro(&mut self) {
            self.skip.on_position(Some(1200.0), Some(INTRO));
        }

        fn offer(&mut self) {
            self.upnext.receive(OFFER);
        }

        /// Receives the offer and moves the playhead near the end, so the
        /// card appears on its own over the hidden OSD.
        fn rise(&mut self) {
            self.offer();
            assert!(
                self.upnext.on_percent(Some(98.0), &self.film, Some(0.0)),
                "moving the playhead to 98 percent did not show the card, want it shown"
            );
        }

        fn card_takes_select(&self) -> bool {
            self.focus.card_takes_select(&self.upnext, &self.skip)
        }

        /// Takes the offer, so the display waits for the next Play.
        fn take(&mut self) {
            self.offer();
            self.upnext.expand();
            self.upnext.take(0.0);
        }

        fn parts(&mut self) -> Parts<'_> {
            Parts {
                presentation: &self.presentation,
                film: &self.film,
                scrubber: &mut self.scrubber,
                strip: &mut self.strip,
                now: self.now,
                upnext: &mut self.upnext,
                skip: &mut self.skip,
                cast: &mut self.cast,
            }
        }

        fn refocus(&mut self) {
            let mut focus = self.focus;
            focus.refocus(&self.parts());
            self.focus = focus;
        }

        fn press(&mut self, action: Action) -> Vec<Command> {
            let mut focus = self.focus;
            let commands = focus.nav(action, &mut self.parts());
            self.focus = focus;
            commands
        }

        fn summon(&mut self) {
            let mut focus = self.focus;
            focus.summon(&self.parts());
            self.focus = focus;
        }

        fn pause(&mut self, paused: bool) {
            let mut focus = self.focus;
            focus.on_pause(paused, &mut self.parts());
            self.focus = focus;
        }
    }

    /// The next block that the sidecar sends for an episode.
    const OFFER: &str = r#"{"reason":"Next in Harbor Lights",
        "title":"E05 \u00b7 The Long Tide",
        "detail":"Harbor Lights \u00b7 S02 \u00b7 45 min"}"#;

    /// An intro from 1100 to 1250 seconds.
    const INTRO: Jump = Jump {
        span: Span {
            kind: Kind::Intro,
            start: 1100.0,
            end: 1250.0,
        },
        to: 1250.0,
    };

    fn skip_to_the_end() -> Vec<Command> {
        vec![vec![json!("seek"), json!(1250.0), json!("absolute+exact")]]
    }

    fn next_and_pause() -> Vec<Command> {
        vec![
            vec![json!("script-message"), json!("liken-next")],
            vec![json!("no-osd"), json!("set"), json!("pause"), json!("yes")],
        ]
    }

    fn pause_toggle() -> Vec<Command> {
        vec![vec![json!("no-osd"), json!("cycle"), json!("pause")]]
    }

    #[test]
    fn a_stop_is_present_when_it_has_something_to_show() {
        let mut display = Display::new("{}");
        assert_eq!(
            Focus::present(&display.parts()),
            vec![Stop::Fine, Stop::Chapter, Stop::Strip]
        );

        display.offer();
        assert_eq!(
            Focus::present(&display.parts()),
            vec![Stop::Next, Stop::Fine, Stop::Chapter, Stop::Strip]
        );

        let mut photo = Display::new(r#"{"type":"image"}"#);
        assert_eq!(
            Focus::present(&photo.parts()),
            vec![Stop::Images, Stop::Strip]
        );

        let mut music = Display::new(r#"{"type":"music"}"#);
        assert_eq!(
            Focus::present(&music.parts()),
            vec![Stop::Fine, Stop::Chapter]
        );

        // A film with no tracks still has the strip's audio offset control.
        let mut bare = Display::new("{}");
        bare.film = Film::default();
        assert_eq!(Focus::present(&bare.parts()), vec![Stop::Strip]);
    }

    /// A summon focuses the first stop below the offer, so select still
    /// plays or pauses.
    #[test]
    fn a_summon_lands_on_the_first_stop_below_the_offer() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        assert!(display.focus.visible(), "the focus is hidden, want visible");
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));

        let mut photo = Display::new(r#"{"type":"image"}"#);
        photo.summon();
        assert_eq!(photo.focus.focused_stop(), Some(Stop::Images));
    }

    #[test]
    fn a_direction_on_a_hidden_osd_only_shows_it() {
        for action in [Action::Up, Action::Down, Action::Left, Action::Right] {
            let mut display = Display::new("{}");
            assert!(
                display.press(action).is_empty(),
                "pressing {action:?} runs a command, want none"
            );
            assert!(display.focus.visible(), "the focus is hidden, want visible");
            assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
            assert!(
                !display.scrubber.scanning(),
                "the scrubber is scanning, want not scanning"
            );
        }
    }

    /// Up and down move through the present stops and stop at each end.
    #[test]
    fn up_and_down_walk_the_present_stops() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();

        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Chapter));
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));
    }

    #[test]
    fn leaving_the_fine_stop_cancels_the_scan() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Right);
        assert!(
            display.scrubber.scanning(),
            "the scrubber is not scanning, want scanning"
        );
        display.press(Action::Down);
        assert!(
            !display.scrubber.scanning(),
            "the scrubber is scanning, want not scanning"
        );
    }

    /// The fine stop scans on left and right and seeks on select. The chapter
    /// stop steps a chapter on left and right, and select there plays or
    /// pauses.
    #[test]
    fn each_stop_handles_the_presses_it_owns() {
        let mut display = Display::new("{}");
        display.summon();

        assert!(
            display.press(Action::Right).is_empty(),
            "pressing Right runs a command, want none"
        );
        assert!(
            display.scrubber.scanning(),
            "the scrubber is not scanning, want scanning"
        );
        assert_eq!(
            display.press(Action::Select),
            vec![vec![json!("seek"), json!(1205.0), json!("absolute+exact")]]
        );
        assert!(
            !display.scrubber.scanning(),
            "the scrubber is scanning, want not scanning"
        );
        assert_eq!(display.press(Action::Select), pause_toggle());

        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Chapter));
        assert_eq!(
            display.press(Action::Right),
            vec![vec![json!("add"), json!("chapter"), json!(1)]]
        );
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    /// The image stop moves through the playlist on left and right, and
    /// select there plays or pauses.
    #[test]
    fn the_image_stop_steps_the_playlist() {
        let mut display = Display::new(r#"{"type":"image"}"#);
        display.summon();
        assert_eq!(display.focus.focused_stop(), Some(Stop::Images));
        assert_eq!(
            display.press(Action::Left),
            vec![vec![json!("playlist-prev")]]
        );
        assert_eq!(
            display.press(Action::Right),
            vec![vec![json!("playlist-next")]]
        );
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    #[test]
    fn the_strip_stop_opens_a_chooser_on_select() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));

        assert!(
            display.press(Action::Select).is_empty(),
            "pressing Select runs a command, want none"
        );
        assert!(
            display.strip.capturing().is_some(),
            "the strip is not capturing a key, want capturing"
        );
    }

    /// A down press moves inside the open chooser, not to another stop, and
    /// a select applies the choice and closes the chooser.
    #[test]
    fn an_open_chooser_captures_every_press_but_back() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);

        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));
        assert_eq!(
            display.press(Action::Select),
            vec![vec![json!("set_property"), json!("aid"), json!("2")]]
        );
        assert!(
            display.strip.capturing().is_none(),
            "the strip is capturing a key, want not capturing"
        );
    }

    /// Back closes an open adjuster and leaves the strip's focus on its
    /// control, so the next select opens the same adjuster.
    #[test]
    fn back_returns_the_focus_to_the_offset_whose_adjuster_it_closed() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Right);
        display.press(Action::Select);
        assert_eq!(display.strip.capturing(), Some(Control::AudioOffset));

        display.press(Action::Back);
        assert_eq!(display.strip.capturing(), None);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));
        display.press(Action::Select);
        assert_eq!(display.strip.capturing(), Some(Control::AudioOffset));
    }

    /// Back closes an open chooser, then cancels a scan, then hides the OSD,
    /// and then ends the run.
    #[test]
    fn back_undoes_one_thing_at_a_time_and_then_ends_the_run() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            display.strip.capturing().is_none(),
            "the strip is capturing a key, want not capturing"
        );
        assert!(display.focus.visible(), "the focus is hidden, want visible");

        display.press(Action::Up);
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        display.press(Action::Right);
        assert!(
            display.scrubber.scanning(),
            "the scrubber is not scanning, want scanning"
        );
        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.scrubber.scanning(),
            "the scrubber is scanning, want not scanning"
        );
        assert!(display.focus.visible(), "the focus is hidden, want visible");

        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
    }

    /// When the film is shrunk beside the offer and the OSD is hidden, the
    /// first back returns the film to the whole screen, and the next back
    /// ends the run.
    #[test]
    fn back_over_the_stepped_back_film_returns_it_first() {
        let mut display = Display::new("{}");
        display.rise();
        display.upnext.step_back();

        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.upnext.stepped_back(),
            "the up-next card has stepped back, want not stepped back"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
    }

    /// While the OSD is shown, the first back hides it, and the film stays
    /// shrunk until the next back.
    #[test]
    fn back_dismisses_the_display_before_it_returns_the_film() {
        let mut display = Display::new("{}");
        display.rise();
        display.upnext.step_back();
        display.summon();

        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
        assert!(
            display.upnext.stepped_back(),
            "the up-next card has not stepped back, want stepped back"
        );

        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.upnext.stepped_back(),
            "the up-next card has stepped back, want not stepped back"
        );
    }

    /// After the offer is taken, back ends the run and leaves the film
    /// shrunk.
    #[test]
    fn back_after_a_take_ends_the_run_with_the_film_back() {
        let mut display = Display::new("{}");
        display.rise();
        display.upnext.step_back();
        assert_eq!(display.press(Action::Select), next_and_pause());

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
        assert!(
            display.upnext.stepped_back(),
            "the up-next card has not stepped back, want stepped back"
        );
    }

    #[test]
    fn after_a_take_every_press_but_back_does_nothing() {
        let mut display = Display::new("{}");
        display.take();
        display.summon();
        for action in [
            Action::Up,
            Action::Down,
            Action::Left,
            Action::Right,
            Action::Select,
        ] {
            assert!(
                display.press(action).is_empty(),
                "pressing {action:?} runs a command, want none"
            );
        }
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert!(
            !display.scrubber.scanning(),
            "the scrubber is scanning, want not scanning"
        );
        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
        assert!(display.focus.visible(), "the focus is hidden, want visible");
    }

    /// A select on a hidden OSD plays or pauses and does not show the OSD.
    #[test]
    fn a_select_on_a_hidden_osd_plays_or_pauses() {
        let mut display = Display::new("{}");
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
    }

    /// A summon arms the hide timer, a dismiss cancels it, and a pause
    /// cancels it.
    #[test]
    fn the_hide_timer_follows_the_summon() {
        let mut display = Display::new("{}");
        display.summon();
        assert_eq!(display.focus.take_hide(), Hide::Arm);
        assert_eq!(display.focus.take_hide(), Hide::Keep);

        display.press(Action::Back);
        assert_eq!(display.focus.take_hide(), Hide::Cancel);

        display.pause(false);
        display.pause(true);
        assert!(display.focus.visible(), "the focus is hidden, want visible");
        assert_eq!(display.focus.take_hide(), Hide::Cancel);
    }

    fn open_the_chooser(display: &mut Display) {
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert_eq!(display.strip.capturing(), Some(Control::Audio));
    }

    /// An open chooser cancels the hide timer, a press inside it does not
    /// arm the timer, and the select that applies a track closes the chooser
    /// and arms the timer.
    #[test]
    fn an_open_chooser_keeps_the_display_shown() {
        let mut display = Display::new("{}");
        open_the_chooser(&mut display);
        assert_eq!(display.focus.take_hide(), Hide::Cancel);

        display.press(Action::Down);
        assert_ne!(display.focus.take_hide(), Hide::Arm);

        display.press(Action::Select);
        assert_eq!(display.strip.capturing(), None);
        assert_eq!(display.focus.take_hide(), Hide::Arm);
    }

    /// An open adjuster cancels the hide timer the same way, and the back
    /// that closes it arms the timer.
    #[test]
    fn an_open_adjuster_keeps_the_display_shown() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Right);
        display.press(Action::Select);
        assert_eq!(display.strip.capturing(), Some(Control::AudioOffset));
        assert_eq!(display.focus.take_hide(), Hide::Cancel);

        display.press(Action::Right);
        assert_ne!(display.focus.take_hide(), Hide::Arm);

        display.press(Action::Back);
        assert_eq!(display.strip.capturing(), None);
        assert_eq!(display.focus.take_hide(), Hide::Arm);
    }

    /// The command sidecar summons the OSD after a seek that it sends. A
    /// summon while a chooser is open must not arm the hide timer, which
    /// would close the list.
    #[test]
    fn a_summon_over_an_open_chooser_arms_nothing() {
        let mut display = Display::new("{}");
        open_the_chooser(&mut display);
        display.focus.take_hide();

        display.summon();
        assert_eq!(display.focus.take_hide(), Hide::Cancel);
    }

    /// While the film is paused, closing a chooser does not arm the hide
    /// timer.
    #[test]
    fn a_chooser_that_closes_over_a_pause_arms_nothing() {
        let mut display = Display::new("{}");
        display.pause(false);
        display.pause(true);
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert!(
            display.strip.capturing().is_some(),
            "the strip is not capturing a key, want capturing"
        );

        display.press(Action::Back);
        assert_eq!(display.focus.take_hide(), Hide::Cancel);
    }

    /// A spans file with one person on screen at the playhead, 1200 seconds.
    const SPANS: &str = r#"{"format":"liken.sh/appearances/spans/v1",
        "people":{"p/pilot":{"name":"Jane Roe"}},
        "spans":[{"start":1190.0,"end":1205.0,"people":["p/pilot"]}]}"#;

    /// A select on the cast row plays or pauses, opens no chooser, does not
    /// cancel the hide timer, and leaves focus on the row.
    #[test]
    fn a_select_on_a_cast_card_opens_nothing() {
        let mut display = Display::new("{}");
        let (item, _) = display
            .cast
            .on_item(Some("spans.json"), None)
            .expect("an item with spans");
        assert!(
            display.cast.on_read(item, crate::cast::Spans::parse(SPANS)),
            "reading the spans did not change the cast, want a change"
        );
        display.summon();
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Cast));
        display.focus.take_hide();

        assert_eq!(display.press(Action::Select), pause_toggle());
        assert_eq!(display.strip.capturing(), None);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Cast));
        assert_ne!(display.focus.take_hide(), Hide::Cancel);
    }

    /// A film that loads paused starts with the OSD hidden, a later pause
    /// shows it, and a resume hides it at once.
    #[test]
    fn a_pause_summons_the_display_and_a_resume_dismisses_it() {
        let mut display = Display::new("{}");
        display.pause(true);
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
        assert!(
            display.focus.paused(),
            "the focus is not paused, want paused"
        );

        display.pause(false);
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
        display.pause(true);
        assert!(display.focus.visible(), "the focus is hidden, want visible");
        display.pause(false);
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
    }

    #[test]
    fn the_axis_follows_the_focus() {
        let mut display = Display::new("{}");
        display.summon();
        assert_eq!(display.focus.axis(), Some(Axis::Fine));
        display.press(Action::Down);
        assert_eq!(display.focus.axis(), Some(Axis::Chapter));
        display.press(Action::Down);
        assert_eq!(display.focus.axis(), None);
        assert_eq!(Focus::new().axis(), None);
    }

    #[test]
    fn a_dismiss_closes_an_open_chooser() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert!(
            display.strip.capturing().is_some(),
            "the strip is not capturing a key, want capturing"
        );

        let mut focus = display.focus;
        focus.dismiss(&mut display.parts());
        display.focus = focus;
        assert!(
            display.strip.capturing().is_none(),
            "the strip is capturing a key, want not capturing"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
    }

    /// With no offer, up from the bar stays on the bar, and select plays or
    /// pauses.
    #[test]
    fn no_offer_has_no_stop_and_never_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    #[test]
    fn left_and_right_on_the_offer_do_nothing() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        for action in [Action::Left, Action::Right] {
            assert!(
                display.press(action).is_empty(),
                "pressing action runs a command, want none"
            );
            assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
            assert!(
                !display.scrubber.scanning(),
                "the scrubber is scanning, want not scanning"
            );
        }
    }

    /// Back on the offer hides the OSD, as on every other stop, and does not
    /// end the run.
    #[test]
    fn back_on_the_offer_hides_the_osd() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        assert!(
            display.press(Action::Back).is_empty(),
            "pressing Back runs a command, want none"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
    }

    /// The first select on the chip expands it into the card and sends no
    /// command, so the viewer sees what the offer starts before a press
    /// starts it.
    #[test]
    fn the_first_select_on_the_chip_expands_it_into_the_card() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        assert!(
            display.press(Action::Select).is_empty(),
            "pressing Select runs a command, want none"
        );
        assert!(
            display.upnext.showing_card(),
            "the up-next card is hidden, want showing"
        );
        assert!(
            !display.upnext.waiting(),
            "the up-next card is waiting, want not waiting"
        );
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
    }

    /// The second select takes the offer.
    #[test]
    fn a_select_on_the_card_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        assert_eq!(display.press(Action::Select), next_and_pause());
        assert!(
            display.upnext.waiting(),
            "the up-next card is not waiting, want waiting"
        );
    }

    /// A card that appeared on its own near the end takes the first select.
    #[test]
    fn a_select_on_the_risen_card_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.offer();
        assert!(
            display
                .upnext
                .on_percent(Some(98.0), &display.film, Some(0.0)),
            "moving the playhead to 98 percent did not show the card, want it shown"
        );
        display.summon();
        display.press(Action::Up);

        assert_eq!(display.press(Action::Select), next_and_pause());
        assert!(
            display.upnext.waiting(),
            "the up-next card is not waiting, want waiting"
        );
    }

    #[test]
    fn leaving_the_offer_collapses_the_card() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        display.press(Action::Down);
        assert!(
            !display.upnext.showing_card(),
            "the up-next card is showing, want hidden"
        );
    }

    #[test]
    fn a_dismiss_collapses_the_card() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        display.press(Action::Back);
        assert!(
            !display.upnext.showing_card(),
            "the up-next card is showing, want hidden"
        );
    }

    /// The offer is above the bar and a summon focuses the bar, so select
    /// reaches the offer only after an up press.
    #[test]
    fn up_reaches_the_offer_and_down_returns() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
    }

    /// The pause that taking the offer sends does not show the OSD.
    #[test]
    fn the_pause_a_take_sends_raises_no_osd() {
        let mut display = Display::new("{}");
        display.pause(false);
        display.rise();
        assert_eq!(display.press(Action::Select), next_and_pause());

        display.pause(true);
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
        assert_eq!(display.focus.fade().value(), 0.0);
    }

    #[test]
    fn after_a_take_back_ends_the_run_from_a_hidden_osd() {
        let mut display = Display::new("{}");
        display.take();
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
    }

    #[test]
    fn the_skip_control_is_a_stop_while_the_playhead_is_inside_the_span() {
        let mut display = Display::new("{}");
        display.offer();
        display.intro();
        assert_eq!(
            Focus::present(&display.parts()),
            vec![
                Stop::Next,
                Stop::Skip,
                Stop::Fine,
                Stop::Chapter,
                Stop::Strip
            ]
        );
    }

    /// A direction that shows the OSD focuses the skip control, so the
    /// select after it skips.
    #[test]
    fn showing_the_osd_focuses_the_skip_control_and_a_select_skips() {
        for action in [Action::Up, Action::Down, Action::Left, Action::Right] {
            let mut display = Display::new("{}");
            display.intro();
            assert!(
                display.press(action).is_empty(),
                "pressing {action:?} runs a command, want none"
            );
            assert_eq!(display.focus.focused_stop(), Some(Stop::Skip), "{action:?}");
            assert_eq!(
                display.press(Action::Select),
                skip_to_the_end(),
                "{action:?}"
            );
            assert!(
                !display.skip.available(),
                "after Select on the skip control opened by {action:?}, the control is available, want unavailable"
            );
            assert_eq!(display.focus.focused_stop(), Some(Stop::Fine), "{action:?}");
        }
    }

    /// While the OSD is hidden, one select skips an intro or a recap, and the
    /// OSD stays hidden.
    #[test]
    fn a_select_at_the_bare_video_skips_an_intro_or_a_recap() {
        for kind in [Kind::Intro, Kind::Recap] {
            let mut display = Display::new("{}");
            display.skip.on_position(
                Some(INTRO.span.start),
                Some(Jump {
                    span: Span { kind, ..INTRO.span },
                    ..INTRO
                }),
            );
            assert_eq!(display.press(Action::Select), skip_to_the_end(), "{kind:?}");
            assert!(
                !display.skip.available(),
                "after Select on {kind:?} the skip control is available, want unavailable"
            );
            assert!(
                !display.focus.visible(),
                "after Select on {kind:?} the focus is visible, want hidden"
            );
        }
    }

    /// Past the first 15 seconds of the intro, the skip control shows only
    /// on the OSD. Up or down shows the OSD with the control focused, and
    /// the select after it skips.
    #[test]
    fn a_direction_after_the_first_fifteen_seconds_focuses_the_skip_control() {
        for action in [Action::Up, Action::Down] {
            let mut display = Display::new("{}");
            display.late_intro();
            assert!(
                display.press(action).is_empty(),
                "pressing {action:?} runs a command, want none"
            );
            assert!(display.focus.visible(), "{action:?} left the OSD hidden");
            assert_eq!(display.focus.focused_stop(), Some(Stop::Skip), "{action:?}");
            assert_eq!(
                display.press(Action::Select),
                skip_to_the_end(),
                "{action:?}"
            );
        }
    }

    /// Past the first 15 seconds of the intro, no control shows over the
    /// bare video, so a select there pauses.
    #[test]
    fn a_select_at_the_bare_video_after_the_first_fifteen_seconds_pauses() {
        let mut display = Display::new("{}");
        display.late_intro();
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(display.skip.available());
    }

    /// While the OSD is hidden, the control that skips the credits draws
    /// unfocused, so a select pauses, for a viewer who wants to read the
    /// credits.
    #[test]
    fn a_select_at_the_bare_video_pauses_in_the_credits() {
        let mut display = Display::new("{}");
        display.skip.on_position(
            Some(5400.0),
            Some(Jump {
                span: Span {
                    kind: Kind::Credits,
                    start: 5300.0,
                    end: 5600.0,
                },
                to: 5600.0,
            }),
        );
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(
            display.skip.available(),
            "the skip control is unavailable, want available"
        );
    }

    /// While the OSD is hidden and no skip control shows, one select takes
    /// the card, and the OSD stays hidden.
    #[test]
    fn a_select_at_the_bare_video_takes_the_risen_card() {
        let mut display = Display::new("{}");
        display.rise();
        assert!(
            display.card_takes_select(),
            "the card does not take Select, want it to"
        );
        assert_eq!(display.press(Action::Select), next_and_pause());
        assert!(
            display.upnext.waiting(),
            "the up-next card is not waiting, want waiting"
        );
        assert!(
            !display.focus.visible(),
            "the focus is visible, want hidden"
        );
    }

    /// After the card's hide timer runs out, a select plays or pauses again.
    #[test]
    fn a_select_at_the_bare_video_pauses_once_the_card_leaves() {
        let mut display = Display::new("{}");
        display.rise();
        display.upnext.hide();
        assert!(
            !display.card_takes_select(),
            "the card takes Select, want it not to"
        );
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(
            !display.upnext.waiting(),
            "the up-next card is waiting, want not waiting"
        );
    }

    /// While the OSD is shown, the card takes select only when it has focus,
    /// and a summon focuses the bar.
    #[test]
    fn a_risen_card_inside_the_osd_takes_select_only_with_focus() {
        let mut display = Display::new("{}");
        display.rise();
        display.summon();
        assert!(
            !display.card_takes_select(),
            "the card takes Select, want it not to"
        );
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(
            !display.upnext.waiting(),
            "the up-next card is waiting, want not waiting"
        );
    }

    /// While the OSD is hidden and a skip control shows beside the card, a
    /// select never takes the card. It skips an intro or a recap, and it
    /// pauses in the credits.
    #[test]
    fn a_select_beside_the_skip_control_leaves_the_card() {
        let cases = [
            (Kind::Intro, skip_to_the_end()),
            (Kind::Recap, skip_to_the_end()),
            (Kind::Credits, pause_toggle()),
        ];
        for (kind, want) in cases {
            let mut display = Display::new("{}");
            display.rise();
            display.skip.on_position(
                Some(INTRO.span.start),
                Some(Jump {
                    span: Span { kind, ..INTRO.span },
                    ..INTRO
                }),
            );
            assert!(
                !display.card_takes_select(),
                "for {kind:?} the card takes Select, want it not to"
            );
            assert_eq!(display.press(Action::Select), want, "{kind:?}");
            assert!(
                !display.upnext.waiting(),
                "after Select on {kind:?} the up-next card is waiting, want not waiting"
            );
        }
    }

    /// A pause focuses the bar below the skip control, so the next select
    /// resumes the film.
    #[test]
    fn a_pause_focuses_below_the_skip_control() {
        let mut display = Display::new("{}");
        display.intro();
        display.pause(false);
        display.pause(true);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    #[test]
    fn up_reaches_the_skip_control_and_it_reacts_only_to_select() {
        let mut display = Display::new("{}");
        display.pause(false);
        display.pause(true);
        display.intro();
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));
        for action in [Action::Left, Action::Right] {
            assert!(
                display.press(action).is_empty(),
                "pressing {action:?} runs a command, want none"
            );
            assert!(
                !display.scrubber.scanning(),
                "after {action:?} the scrubber is scanning, want not scanning"
            );
        }
        assert_eq!(display.press(Action::Select), skip_to_the_end());
    }

    /// When the playhead leaves the span, focus moves from the skip control
    /// to the bar, and a select there plays or pauses.
    #[test]
    fn the_focus_leaves_the_control_with_the_span() {
        let mut display = Display::new("{}");
        display.intro();
        display.summon();
        assert_eq!(display.focus.focused_stop(), Some(Stop::Skip));

        display.skip.on_position(Some(1250.0), None);
        display.refocus();
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert_eq!(display.press(Action::Select), pause_toggle());

        // Focus on a stop that is still present does not move.
        display.press(Action::Down);
        display.refocus();
        assert_eq!(display.focus.focused_stop(), Some(Stop::Chapter));
    }

    /// `Focus::default()` must also skip mpv's first pause report, or the
    /// OSD would show when a film loads paused.
    #[test]
    fn default_equals_new() {
        assert_eq!(Focus::default(), Focus::new());
    }

    #[test]
    fn from_word_reads_only_the_press_names() {
        assert_eq!(Action::from_word("up"), Some(Action::Up));
        assert_eq!(Action::from_word("down"), Some(Action::Down));
        assert_eq!(Action::from_word("left"), Some(Action::Left));
        assert_eq!(Action::from_word("right"), Some(Action::Right));
        assert_eq!(Action::from_word("select"), Some(Action::Select));
        assert_eq!(Action::from_word("back"), Some(Action::Back));
        assert_eq!(Action::from_word("summon"), None);
        assert_eq!(Action::from_word(""), None);
    }
}
