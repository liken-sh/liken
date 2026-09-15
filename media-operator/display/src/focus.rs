//! The input router and the summon state of the display. It holds which stop
//! has focus and whether a chooser captures, and it sends each press to the
//! right module. It draws no pixels.

use serde_json::json;

use crate::fade::{Clock, Fade, Hide};
use crate::film::Film;
use crate::images;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::scrubber::{self, Axis, Scrubber};
use crate::strip::Strip;
use crate::upnext::UpNext;

/// The six words the command sidecar sends, and the whole of what the display
/// answers.
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

/// The focus stops, top to bottom. The scrubber owns two of them, fine and
/// chapter, on one bar. up and down walk the stops present for the current file
/// and skip the rest, so the list is flat and dynamic.
/// The up-next offer is the stop above the scrubber, so up from the fine axis
/// lands on it and down returns.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Stop {
    Next,
    Fine,
    Chapter,
    Images,
    Strip,
}

const STOPS: [Stop; 5] = [
    Stop::Next,
    Stop::Fine,
    Stop::Chapter,
    Stop::Images,
    Stop::Strip,
];

/// The script-message the display and the command sidecar agree on for the exit
/// press. The sidecar answers it: it publishes the ending to the bus and then
/// quits mpv. The display does not quit mpv itself, because the operator must
/// read the ending while the film is still on the display. It then draws the
/// idle screen over the film that is shutting down, with no black gap between
/// the two.
const EXIT: &str = "liken-exit";

/// The script-message a select on the up-next offer broadcasts. The command
/// sidecar reads it and publishes the Play's own request block on the Player's
/// commands topic, and the browser starts the next work.
const NEXT: &str = "liken-next";

/// The modules one press reaches, and the film they read.
pub struct Parts<'a> {
    pub presentation: &'a Presentation,
    pub film: &'a Film,
    pub scrubber: &'a mut Scrubber,
    pub strip: &'a mut Strip,
    /// The monotonic second the seek ramp reads.
    pub now: f64,
    pub upnext: &'a mut UpNext,
}

/// Whether the display stands summoned, where the focus is, and how far the
/// fade has moved.
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
    /// The display starts clear, and the first pause callback only records the
    /// state mpv is already in.
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

    pub fn focused_stop(self) -> Option<Stop> {
        self.focused
    }

    /// The fade factor, so the frame loop draws the layout while it is above 0
    /// and draws nothing once it reaches 0.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    pub fn paused(self) -> bool {
        self.paused
    }

    /// The idle window this press asks for, which the frame loop arms and
    /// cancels because it owns the timer.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    /// Which axis of the bar has focus, and none when the focus is elsewhere.
    pub fn axis(self) -> Option<Axis> {
        match self.focused {
            Some(Stop::Fine) => Some(Axis::Fine),
            Some(Stop::Chapter) => Some(Axis::Chapter),
            _ => None,
        }
    }

    /// A stop takes focus only when it has something to show, so up and down
    /// walk the present stops and skip the rest.
    pub fn present(parts: &Parts<'_>) -> Vec<Stop> {
        STOPS
            .into_iter()
            .filter(|stop| Self::stop_available(*stop, parts))
            .collect()
    }

    fn stop_available(stop: Stop, parts: &Parts<'_>) -> bool {
        let film = parts.film;
        match stop {
            Stop::Next => parts.upnext.available(),
            Stop::Fine => !parts.presentation.is_image() && scrubber::fine_available(film),
            Stop::Chapter => !parts.presentation.is_image() && scrubber::chapter_available(film),
            Stop::Images => images::available(parts.presentation),
            Stop::Strip => Strip::available(parts.presentation, film),
        }
    }

    /// Show the display and arm the idle window.
    pub fn summon(&mut self, parts: &Parts<'_>) {
        if !self.summoned {
            self.summoned = true;
            self.reset_focus(parts);
        }
        // A summon while paused arms nothing, so the display stands for as
        // long as the film does.
        self.clock.show(match self.paused {
            true => Hide::Cancel,
            false => Hide::Arm,
        });
    }

    /// Clear the display and cancel the idle window.
    pub fn dismiss(&mut self, parts: &mut Parts<'_>) {
        self.summoned = false;
        parts.upnext.collapse();
        parts.strip.close();
        self.clock.ask(Hide::Cancel);
        self.clock.hide();
    }

    /// A summon lands on the first stop below the offer, so the main button
    /// stays play-pause, and a select on the offer takes an up press first.
    fn reset_focus(&mut self, parts: &Parts<'_>) {
        self.focused = Self::present(parts)
            .into_iter()
            .find(|stop| *stop != Stop::Next);
    }

    /// The frame loop observes pause and reports it here. A pause summons the
    /// display and holds it, and a resume dismisses it at once.
    ///
    /// mpv reports the pause state once at registration. The first report only
    /// records it, so a film that loads paused starts with the display hidden.
    /// A later pause, during playback, summons the display.
    pub fn on_pause(&mut self, paused: bool, parts: &mut Parts<'_>) {
        self.paused = paused;
        if self.first_pause {
            self.first_pause = false;
            return;
        }
        if paused {
            self.summon(parts);
            self.clock.ask(Hide::Cancel);
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
        // Leaving the fine stop drops a scan in flight, so a preview does not
        // outlive the move to another control.
        if self.focused == Some(Stop::Fine) && next != Stop::Fine {
            parts.scrubber.cancel();
        }
        // An expanded card is part of the stop, so leaving the stop collapses
        // it.
        if self.focused == Some(Stop::Next) && next != Stop::Next {
            parts.upnext.collapse();
        }
        self.focused = Some(next);
    }

    /// Route the horizontal presses to the focused stop. The fine and chapter
    /// stops seek and step on the scrubber. The strip moves its control focus,
    /// and select opens a chooser that captures.
    fn route(&mut self, action: Action, parts: &mut Parts<'_>) -> Vec<Command> {
        match self.focused {
            // The offer is one thing to take, so it answers select and nothing
            // else.
            Some(Stop::Next) | None => Vec::new(),
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

    /// select carries the main action and play/pause on one button. It acts on
    /// an open chooser, the focused strip control, or a fine scan in flight, and
    /// otherwise toggles play/pause. So the button confirms a choice when the
    /// display has one to make, and plays or pauses the film when it does not.
    fn select(&mut self, parts: &mut Parts<'_>) -> Vec<Command> {
        if parts.strip.capturing().is_some() {
            return parts.strip.handle(Action::Select, parts.film);
        }
        if self.summoned {
            if self.focused == Some(Stop::Next) {
                // The first select on the chip grows it into the card, and a
                // select on the card sends the ask. So the offer shows what it
                // starts before a press starts it. A card the rise raised takes
                // one press.
                if parts.upnext.showing_card() {
                    parts.upnext.take();
                    return vec![next()];
                }
                parts.upnext.expand();
                return Vec::new();
            }
            if self.focused == Some(Stop::Strip) {
                return self.route(Action::Select, parts);
            }
            if self.focused == Some(Stop::Fine) && parts.scrubber.scanning() {
                return parts.scrubber.commit();
            }
        }
        // Toggle mpv's pause. The pause observer summons the display, so a
        // pause from select needs no separate summon. The no-osd prefix
        // suppresses mpv's own pause indicator, so the liken display is the
        // only thing that draws.
        vec![vec![json!("no-osd"), json!("cycle"), json!("pause")]]
    }

    /// back has one meaning per state, tried in order. An open chooser closes.
    /// Else a fine scan cancels its preview. Else the visible display
    /// dismisses. Else, at the bare video, back asks the command sidecar to end
    /// the run, and the sidecar quits mpv with code 0, so the pod ends as the
    /// Completed a finished film gives, not an Error.
    pub fn nav(&mut self, action: Action, parts: &mut Parts<'_>) -> Vec<Command> {
        // The offer is taken and the next Play is starting, so the display
        // routes every press to nothing. back still ends the run, on the same
        // message the bare video sends, because a person must be able to leave.
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
                // A scan is in flight, so back cancels the preview and leaves
                // the video where it plays.
                parts.scrubber.cancel();
            } else if self.summoned {
                self.dismiss(parts);
            } else {
                return vec![exit()];
            }
            return Vec::new();
        }

        if action == Action::Select {
            return self.select(parts);
        }

        // The press that wakes a hidden display only wakes it. It lands focus
        // on the first stop present, and does not move or seek. The next press
        // starts navigating, so a viewer sees where the film is before a press
        // changes it.
        let was_visible = self.summoned;
        self.summon(parts);
        if !was_visible {
            return Vec::new();
        }

        // A captured widget receives up, down, left, and right. A chooser is a
        // vertical list, so it moves on up and down and ignores left and right.
        // The delay adjuster uses left and right to nudge a delay. back, handled
        // above, closes the widget.
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

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    /// One film, one presentation, and the two modules a press reaches.
    struct Display {
        presentation: Presentation,
        film: Film,
        scrubber: Scrubber,
        strip: Strip,
        focus: Focus,
        now: f64,
        upnext: UpNext,
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
            }
        }

        /// The block the sidecar sends for the work that follows this one.
        fn offer(&mut self) {
            self.upnext.receive(OFFER);
        }

        /// The offer taken, which is the rise, a select on the card, and the
        /// wait that follows.
        fn take(&mut self) {
            self.offer();
            self.upnext.expand();
            self.upnext.take();
        }

        fn parts(&mut self) -> Parts<'_> {
            Parts {
                presentation: &self.presentation,
                film: &self.film,
                scrubber: &mut self.scrubber,
                strip: &mut self.strip,
                now: self.now,
                upnext: &mut self.upnext,
            }
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

    /// The block the sidecar sends for an episode.
    const OFFER: &str = r#"{"reason":"Next in Harbor Lights",
        "title":"E05 \u00b7 The Long Tide",
        "detail":"Harbor Lights \u00b7 S02 \u00b7 45 min"}"#;

    fn ask() -> Vec<Command> {
        vec![vec![json!("script-message"), json!("liken-next")]]
    }

    fn pause_toggle() -> Vec<Command> {
        vec![vec![json!("no-osd"), json!("cycle"), json!("pause")]]
    }

    /// A film with a length, chapters, and tracks carries four of the five
    /// stops, and a still photo carries the images stop instead.
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

        // A film with no tracks at all still carries the audio offset, so the
        // strip is the one stop it has.
        let mut bare = Display::new("{}");
        bare.film = Film::default();
        assert_eq!(Focus::present(&bare.parts()), vec![Stop::Strip]);
    }

    /// A summon lands on the first stop below the offer, so the main button
    /// stays play-pause.
    #[test]
    fn a_summon_lands_on_the_first_stop_below_the_offer() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        assert!(display.focus.visible());
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));

        let mut photo = Display::new(r#"{"type":"image"}"#);
        photo.summon();
        assert_eq!(photo.focus.focused_stop(), Some(Stop::Images));
    }

    /// The first press wakes the display and moves nothing.
    #[test]
    fn the_press_that_wakes_the_display_only_wakes_it() {
        for action in [Action::Up, Action::Down, Action::Left, Action::Right] {
            let mut display = Display::new("{}");
            assert!(
                display.press(action).is_empty(),
                "{action:?} runs no command"
            );
            assert!(display.focus.visible());
            assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
            assert!(!display.scrubber.scanning());
        }
    }

    /// up and down walk the present stops and hold at the ends of the list.
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

    /// Leaving the fine stop drops a scan in flight.
    #[test]
    fn leaving_the_fine_stop_drops_the_scan() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Right);
        assert!(display.scrubber.scanning());
        display.press(Action::Down);
        assert!(!display.scrubber.scanning());
    }

    /// The fine stop scans on left and right and seeks on select. The chapter
    /// stop steps a chapter and answers select with play-pause.
    #[test]
    fn each_stop_answers_the_presses_it_owns() {
        let mut display = Display::new("{}");
        display.summon();

        assert!(display.press(Action::Right).is_empty());
        assert!(display.scrubber.scanning());
        assert_eq!(
            display.press(Action::Select),
            vec![vec![json!("seek"), json!(1205.0), json!("absolute+exact")]]
        );
        assert!(!display.scrubber.scanning());
        assert_eq!(display.press(Action::Select), pause_toggle());

        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Chapter));
        assert_eq!(
            display.press(Action::Right),
            vec![vec![json!("add"), json!("chapter"), json!(1)]]
        );
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    /// The image stop steps the playlist on left and right, and select there
    /// plays or pauses.
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

    /// The strip moves its own focus on left and right, and select opens the
    /// focused control's chooser.
    #[test]
    fn the_strip_stop_opens_a_chooser_on_select() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Strip));

        assert!(display.press(Action::Select).is_empty());
        assert!(display.strip.capturing().is_some());
    }

    /// A chooser captures up, down, left, and right, and a select applies and
    /// closes it.
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
        assert!(display.strip.capturing().is_none());
    }

    /// back closes an open chooser, then cancels a scan, then dismisses the
    /// display, and at the bare video it ends the run.
    #[test]
    fn back_has_one_meaning_per_state() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert!(display.press(Action::Back).is_empty());
        assert!(display.strip.capturing().is_none());
        assert!(display.focus.visible());

        display.press(Action::Up);
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        display.press(Action::Right);
        assert!(display.scrubber.scanning());
        assert!(display.press(Action::Back).is_empty());
        assert!(!display.scrubber.scanning());
        assert!(display.focus.visible());

        assert!(display.press(Action::Back).is_empty());
        assert!(!display.focus.visible());

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
    }

    /// While the display waits on the next Play it routes every press to
    /// nothing, and back ends the run whether the display is up or down.
    #[test]
    fn the_waiting_gate_swallows_every_press_but_back() {
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
                "{action:?} runs no command"
            );
        }
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert!(!display.scrubber.scanning());
        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
        assert!(display.focus.visible());
    }

    /// A select on a hidden display plays or pauses, and summons nothing of
    /// its own.
    #[test]
    fn a_select_on_a_hidden_display_plays_or_pauses() {
        let mut display = Display::new("{}");
        assert_eq!(display.press(Action::Select), pause_toggle());
        assert!(!display.focus.visible());
    }

    /// A summon arms the idle window, a dismiss cancels it, and a summon while
    /// paused arms nothing.
    #[test]
    fn the_idle_window_follows_the_summon() {
        let mut display = Display::new("{}");
        display.summon();
        assert_eq!(display.focus.take_hide(), Hide::Arm);
        assert_eq!(display.focus.take_hide(), Hide::Keep);

        display.press(Action::Back);
        assert_eq!(display.focus.take_hide(), Hide::Cancel);

        display.pause(false);
        display.pause(true);
        assert!(display.focus.visible());
        assert_eq!(display.focus.take_hide(), Hide::Cancel);
    }

    /// A film that loads paused starts with the display hidden, a later pause
    /// summons it, and a resume dismisses it at once.
    #[test]
    fn a_pause_summons_the_display_and_a_resume_dismisses_it() {
        let mut display = Display::new("{}");
        display.pause(true);
        assert!(!display.focus.visible());
        assert!(display.focus.paused());

        display.pause(false);
        assert!(!display.focus.visible());
        display.pause(true);
        assert!(display.focus.visible());
        display.pause(false);
        assert!(!display.focus.visible());
    }

    /// The bar's axis follows the focus, and no other stop brightens either
    /// group.
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

    /// A dismiss closes an open chooser, so the next summon shows the strip
    /// and not the list.
    #[test]
    fn a_dismiss_closes_an_open_chooser() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Down);
        display.press(Action::Down);
        display.press(Action::Select);
        assert!(display.strip.capturing().is_some());

        let mut focus = display.focus;
        focus.dismiss(&mut display.parts());
        display.focus = focus;
        assert!(display.strip.capturing().is_none());
        assert!(!display.focus.visible());
    }

    /// With no offer there is no stop above the bar, and the select that
    /// would take it plays or pauses the film instead.
    #[test]
    fn no_offer_has_no_stop_and_never_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.summon();
        display.press(Action::Up);
        assert_eq!(display.focus.focused_stop(), Some(Stop::Fine));
        assert_eq!(display.press(Action::Select), pause_toggle());
    }

    /// The offer answers select and nothing else, so a horizontal press on it
    /// moves no focus and starts no scan.
    #[test]
    fn left_and_right_on_the_offer_do_nothing() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        for action in [Action::Left, Action::Right] {
            assert!(display.press(action).is_empty());
            assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
            assert!(!display.scrubber.scanning());
        }
    }

    /// back on the offer dismisses the display, as it does on every other
    /// stop, and does not end the run.
    #[test]
    fn back_on_the_offer_dismisses_the_display() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        assert!(display.press(Action::Back).is_empty());
        assert!(!display.focus.visible());
    }

    /// The first select on the chip grows it into the card and asks for
    /// nothing, so the offer shows what it starts before a press starts it.
    #[test]
    fn the_first_select_on_the_chip_grows_it_into_the_card() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);

        assert!(display.press(Action::Select).is_empty());
        assert!(display.upnext.showing_card());
        assert!(!display.upnext.waiting());
        assert_eq!(display.focus.focused_stop(), Some(Stop::Next));
    }

    /// The select after that sends the ask and starts the wait.
    #[test]
    fn a_select_on_the_card_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        assert_eq!(display.press(Action::Select), ask());
        assert!(display.upnext.waiting());
    }

    /// A card the rise raised takes one press.
    #[test]
    fn a_select_on_the_risen_card_asks_for_the_next_work() {
        let mut display = Display::new("{}");
        display.offer();
        assert!(display.upnext.on_percent(Some(98.0), &display.film));
        display.summon();
        display.press(Action::Up);

        assert_eq!(display.press(Action::Select), ask());
        assert!(display.upnext.waiting());
    }

    /// An expansion is part of the stop, so leaving the stop collapses it.
    #[test]
    fn leaving_the_offer_collapses_the_expansion() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        display.press(Action::Down);
        assert!(!display.upnext.showing_card());
    }

    /// A hidden display collapses the expansion too, so a summon shows the
    /// chip again.
    #[test]
    fn a_dismiss_collapses_the_expansion() {
        let mut display = Display::new("{}");
        display.offer();
        display.summon();
        display.press(Action::Up);
        display.press(Action::Select);

        display.press(Action::Back);
        assert!(!display.upnext.showing_card());
    }

    /// The offer's stop stands above the fine axis, and a summon lands below
    /// it, so a select reaches the offer only after an up press.
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

    /// The waiting gate ends the run from a hidden display as well.
    #[test]
    fn the_waiting_gate_ends_the_run_from_a_hidden_display() {
        let mut display = Display::new("{}");
        display.take();
        assert!(!display.focus.visible());

        assert_eq!(
            display.press(Action::Back),
            vec![vec![json!("script-message"), json!("liken-exit")]]
        );
    }

    /// A focus built either way holds the same state, so one built through
    /// the derive never summons the display on mpv's first pause report.
    #[test]
    fn a_focus_reads_the_same_whichever_way_it_is_built() {
        assert_eq!(Focus::default(), Focus::new());
    }

    #[test]
    fn the_six_words_are_the_whole_of_what_a_press_can_be() {
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
