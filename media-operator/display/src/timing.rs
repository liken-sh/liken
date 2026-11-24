//! The delay of the audio or of the subtitles, which the timing row at the
//! top of a track panel sets. Left shows the track one step sooner and
//! right one step later, and select compares the delay with the file's
//! own timing. mpv applies a new delay at once, so no key confirms it.
//!
//! A positive `audio-delay` or `sub-delay` plays the track later, so the
//! words and the scale read sooner at the left and later at the right, as
//! the scrubber reads earlier at the left. The display knows the delay and
//! cannot observe whether a track is in sync, so no word claims it.

use serde_json::json;

use crate::film::Film;
use crate::ipc::Command;

/// One of the two mpv delay properties.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Delay {
    Audio,
    Subtitles,
}

impl Delay {
    pub fn property(self) -> &'static str {
        match self {
            Delay::Audio => "audio-delay",
            Delay::Subtitles => "sub-delay",
        }
    }

    /// The word that starts the timing row, the name of the strip's stop.
    fn noun(self) -> &'static str {
        match self {
            Delay::Audio => "Audio",
            Delay::Subtitles => "Subtitles",
        }
    }

    /// The size of one step, in seconds. The audio delay corrects the
    /// latency of a receiver or a television, and people hear audio that
    /// is a few tens of milliseconds off, so the audio step is small.
    /// People notice subtitle timing from about 100 ms off, so a smaller
    /// subtitle step would only add key presses.
    fn step(self) -> f64 {
        match self {
            Delay::Audio => 0.05,
            Delay::Subtitles => 0.1,
        }
    }

    /// The largest delay in either direction, in seconds. Receiver latency
    /// stays well under 5 s. A subtitle track made for a different cut of
    /// the video can be several seconds off, so its range is larger.
    pub fn range(self) -> f64 {
        match self {
            Delay::Audio => 5.0,
            Delay::Subtitles => 10.0,
        }
    }

    /// The decimal places of a delay in words, the places of one step.
    fn places(self) -> usize {
        match self {
            Delay::Audio => 2,
            Delay::Subtitles => 1,
        }
    }

    /// The current delay in seconds, as mpv last reported it.
    pub fn delay(self, film: &Film) -> f64 {
        match self {
            Delay::Audio => film.audio_delay,
            Delay::Subtitles => film.sub_delay,
        }
    }

    /// Whether a track of this kind plays, so its delay changes what the
    /// viewer sees or hears. With subtitles off, the timing row is faint
    /// and its keys change nothing.
    pub fn active(self, film: &Film) -> bool {
        let kind = match self {
            Delay::Audio => "audio",
            Delay::Subtitles => "sub",
        };
        film.tracks_of(kind).any(|track| track.selected)
    }

    /// Whether `at` reads as no delay. mpv reports the value that the
    /// display set, which is rounded to a millisecond, so anything under
    /// half a millisecond is zero.
    fn is_zero(at: f64) -> bool {
        at.abs() < 0.0005
    }

    /// The delay in words, such as `0.7 s sooner` or `0.10 s later`, or
    /// `None` at zero.
    pub fn words(self, at: f64) -> Option<String> {
        if Self::is_zero(at) {
            return None;
        }
        let direction = if at < 0.0 { "sooner" } else { "later" };
        Some(format!("{:.*} s {direction}", self.places(), at.abs()))
    }

    /// The readout over the scale: the heading while a track of this kind
    /// plays, and `Subtitles off` while none does, because then the delay
    /// changes nothing on the screen.
    pub fn reading(self, film: &Film) -> String {
        match self.active(film) {
            true => self.heading(self.delay(film)),
            false => format!("{} off", self.noun()),
        }
    }

    /// The delays that the scale marks with a short tick each way, in
    /// seconds: the values a viewer thinks in. The scale has no numerals,
    /// so the ticks show the size of a delay and that the scale is not
    /// linear.
    pub fn graduations(self) -> &'static [f64] {
        match self {
            Delay::Audio => &[0.1, 0.25, 0.5, 1.0],
            Delay::Subtitles => &[0.5, 1.0, 2.0, 5.0],
        }
    }

    /// Whether the delay is at the end of the range in `direction` (-1.0
    /// sooner, 1.0 later), so a step that way changes nothing.
    pub fn at_end(self, at: f64, direction: f64) -> bool {
        (at * direction) >= self.range() - 0.0005
    }

    /// The words of the timing row: what moves and by how much, such as
    /// `Subtitles 0.7 s sooner`, and `Subtitles as timed in the file` at
    /// zero.
    pub fn heading(self, at: f64) -> String {
        match self.words(at) {
            Some(words) => format!("{} {words}", self.noun()),
            None => format!("{} as timed in the file", self.noun()),
        }
    }

    /// Where the scale's mark is, from -1 at the sooner end to 1 at the
    /// later end. The distance from zero is the square root of the delay's
    /// share of the range, so the small delays that people set most often
    /// move the mark far enough to see. On a linear scale, one audio step
    /// would move the mark 3 pixels.
    pub fn share(self, at: f64) -> f32 {
        let share = (at.abs() / self.range()).min(1.0).sqrt();
        (share * at.signum()) as f32
    }

    /// The delay one step from `at` in `direction` (1.0 or -1.0), clamped
    /// to the range. It rounds to a whole millisecond so that repeated
    /// steps do not accumulate floating-point error.
    fn nudge(self, at: f64, direction: f64) -> f64 {
        let next = ((at + direction * self.step()) * 1000.0).round() / 1000.0;
        next.clamp(-self.range(), self.range())
    }

    fn set(self, value: f64) -> Vec<Command> {
        vec![vec![
            json!("set_property"),
            json!(self.property()),
            json!(value),
        ]]
    }
}

/// The delay that a compare set aside for each kind, which the next
/// select on the timing row sets back.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Timing {
    audio: Option<f64>,
    subtitles: Option<f64>,
}

impl Timing {
    fn set_aside(&mut self, delay: Delay) -> &mut Option<f64> {
        match delay {
            Delay::Audio => &mut self.audio,
            Delay::Subtitles => &mut self.subtitles,
        }
    }

    /// Moves the delay one step sooner (-1.0) or later (1.0). A step
    /// forgets the delay that a compare set aside, because the viewer has
    /// chosen to move on from it. At the end of the range a step that way
    /// sends nothing.
    pub fn step(&mut self, delay: Delay, direction: f64, film: &Film) -> Vec<Command> {
        if !delay.active(film) || delay.at_end(delay.delay(film), direction) {
            return Vec::new();
        }
        *self.set_aside(delay) = None;
        delay.set(delay.nudge(delay.delay(film), direction))
    }

    /// Compares the delay with the file's own timing. A viewer can tell
    /// whether a correction went the right way only by watching a line
    /// with it and without it. So a select sets a delay to zero and sets
    /// the delay aside, and the next select sets it back.
    pub fn compare(&mut self, delay: Delay, film: &Film) -> Vec<Command> {
        if !delay.active(film) {
            return Vec::new();
        }
        let at = delay.delay(film);
        if Delay::is_zero(at) {
            return match self.set_aside(delay).take() {
                Some(back) => delay.set(back),
                None => Vec::new(),
            };
        }
        *self.set_aside(delay) = Some(at);
        delay.set(0.0)
    }

    /// The delay that the next select sets back, while a compare has the
    /// delay at zero.
    pub fn returns_to(&self, delay: Delay, film: &Film) -> Option<f64> {
        let set_aside = match delay {
            Delay::Audio => self.audio,
            Delay::Subtitles => self.subtitles,
        };
        set_aside.filter(|_| delay.active(film) && Delay::is_zero(delay.delay(film)))
    }

    /// The line that says what a select on the timing row does, or `None`
    /// when it would do nothing. While a compare has the delay at zero,
    /// the line names the delay that the next select sets back, so the
    /// viewer can find the way back whichever row has the focus. Otherwise
    /// it shows only on the focused timing row of a delay that is not
    /// zero.
    pub fn hint(&self, delay: Delay, film: &Film, focused: bool) -> Option<String> {
        if !delay.active(film) {
            return None;
        }
        let at = delay.delay(film);
        let set_aside = match delay {
            Delay::Audio => self.audio,
            Delay::Subtitles => self.subtitles,
        };
        match (Delay::is_zero(at), set_aside) {
            (true, Some(back)) => delay
                .words(back)
                .map(|words| format!("Select returns to {words}")),
            (false, _) if focused => Some("Select shows the file's timing".to_string()),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A film that plays an audio track and a subtitle track, with the
    /// given delays.
    fn film(audio: f64, sub: f64) -> Film {
        let mut film = Film::default();
        film.apply("audio-delay", &json!(audio));
        film.apply("sub-delay", &json!(sub));
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 1, "type": "sub", "lang": "eng", "selected": true },
            ]),
        );
        film
    }

    /// The film with its subtitles off.
    fn subtitles_off(sub: f64) -> Film {
        let mut film = film(0.0, sub);
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 1, "type": "sub", "lang": "eng" },
            ]),
        );
        film
    }

    fn written(commands: &[Command]) -> Option<f64> {
        commands.first().and_then(|command| command[2].as_f64())
    }

    /// A subtitle delay has one decimal and an audio delay two, the
    /// places of their steps.
    #[test]
    fn the_timing_row_names_what_moves_and_by_how_much() {
        let cases = [
            (Delay::Subtitles, -0.7, "Subtitles 0.7 s sooner"),
            (Delay::Subtitles, 1.2, "Subtitles 1.2 s later"),
            (Delay::Subtitles, -10.0, "Subtitles 10.0 s sooner"),
            (Delay::Subtitles, 0.0, "Subtitles as timed in the file"),
            (Delay::Subtitles, 0.0004, "Subtitles as timed in the file"),
            (Delay::Audio, 0.1, "Audio 0.10 s later"),
            (Delay::Audio, -0.05, "Audio 0.05 s sooner"),
            (Delay::Audio, 0.0, "Audio as timed in the file"),
        ];
        for (delay, at, heading) in cases {
            assert_eq!(delay.heading(at), heading, "{delay:?} at {at}");
        }
        assert_eq!(Delay::Subtitles.words(0.0), None);
    }

    /// A subtitle delay of 0.7 s puts the mark at the square root of
    /// 0.07 of the half scale, where a linear scale would put it at 0.07.
    #[test]
    fn the_mark_is_at_the_square_root_of_the_delays_share() {
        let cases = [
            (Delay::Subtitles, 0.0, 0.0),
            (Delay::Subtitles, -0.7, -0.2646),
            (Delay::Subtitles, 2.5, 0.5),
            (Delay::Subtitles, 10.0, 1.0),
            (Delay::Audio, 0.05, 0.1),
            (Delay::Audio, -5.0, -1.0),
        ];
        for (delay, at, share) in cases {
            assert!(
                (delay.share(at) - share).abs() < 0.0001,
                "the share of {at} s of {delay:?} is {}, want {share}",
                delay.share(at)
            );
        }
    }

    #[test]
    fn each_delay_steps_by_its_own_step_and_stops_at_its_range() {
        let cases = [
            (Delay::Audio, 0.0, 1.0, 0.05),
            (Delay::Audio, 0.35, -1.0, 0.3),
            (Delay::Subtitles, 0.0, -1.0, -0.1),
            (Delay::Subtitles, 0.2, 1.0, 0.3),
            (Delay::Subtitles, -5.0, -1.0, -5.1),
        ];
        for (delay, at, direction, want) in cases {
            let film = match delay {
                Delay::Audio => film(at, 0.0),
                Delay::Subtitles => film(0.0, at),
            };
            let commands = Timing::default().step(delay, direction, &film);
            assert_eq!(
                commands,
                vec![vec![
                    json!("set_property"),
                    json!(delay.property()),
                    json!(want)
                ]],
                "a step of {direction} from {at} s of {delay:?}"
            );
        }
    }

    /// At the end of the range, a step that way sends nothing, and a step
    /// back moves the delay.
    #[test]
    fn a_step_past_the_end_of_the_range_sends_nothing() {
        let mut timing = Timing::default();
        assert!(timing.step(Delay::Audio, 1.0, &film(5.0, 0.0)).is_empty());
        assert!(
            timing
                .step(Delay::Subtitles, -1.0, &film(0.0, -10.0))
                .is_empty()
        );
        assert_eq!(
            written(&timing.step(Delay::Subtitles, 1.0, &film(0.0, -10.0))),
            Some(-9.9)
        );
        assert!(Delay::Subtitles.at_end(-10.0, -1.0));
        assert!(!Delay::Subtitles.at_end(-10.0, 1.0));
    }

    #[test]
    fn the_readout_says_when_the_track_it_times_is_off() {
        assert_eq!(
            Delay::Subtitles.reading(&subtitles_off(0.3)),
            "Subtitles off"
        );
        assert_eq!(
            Delay::Subtitles.reading(&film(0.0, 0.3)),
            "Subtitles 0.3 s later"
        );
        assert_eq!(
            Delay::Audio.reading(&subtitles_off(0.0)),
            "Audio as timed in the file"
        );
    }

    /// The set-aside delay has a place on the scale only while the compare
    /// holds the delay at zero.
    #[test]
    fn the_set_aside_delay_shows_while_the_compare_holds_zero() {
        let mut timing = Timing::default();
        timing.compare(Delay::Subtitles, &film(0.0, -0.7));
        assert_eq!(
            timing.returns_to(Delay::Subtitles, &film(0.0, 0.0)),
            Some(-0.7)
        );
        assert_eq!(timing.returns_to(Delay::Subtitles, &film(0.0, 0.1)), None);
        assert_eq!(timing.returns_to(Delay::Audio, &film(0.0, 0.0)), None);
    }

    /// A select sets the delay to zero and sets it aside, and the next
    /// select sets it back. A step forgets it.
    #[test]
    fn a_compare_sets_the_delay_aside_and_the_next_select_sets_it_back() {
        let mut timing = Timing::default();
        assert_eq!(
            written(&timing.compare(Delay::Subtitles, &film(0.0, -0.7))),
            Some(0.0)
        );
        assert_eq!(
            written(&timing.compare(Delay::Subtitles, &film(0.0, 0.0))),
            Some(-0.7)
        );
        assert!(
            timing.compare(Delay::Subtitles, &film(0.0, 0.0)).is_empty(),
            "a select at zero with nothing set aside sends a command, want none"
        );

        timing.compare(Delay::Subtitles, &film(0.0, -0.7));
        timing.step(Delay::Subtitles, -1.0, &film(0.0, 0.0));
        assert!(
            timing.compare(Delay::Subtitles, &film(0.0, 0.0)).is_empty(),
            "after a step, a select at zero sets back -0.7, want nothing because the step forgot it"
        );
    }

    /// The audio and the subtitles each keep their own delay set aside.
    #[test]
    fn each_delay_sets_aside_its_own_value() {
        let mut timing = Timing::default();
        timing.compare(Delay::Audio, &film(0.1, 0.0));
        timing.compare(Delay::Subtitles, &film(0.0, 1.5));
        assert_eq!(
            written(&timing.compare(Delay::Audio, &film(0.0, 0.0))),
            Some(0.1)
        );
        assert_eq!(
            written(&timing.compare(Delay::Subtitles, &film(0.0, 0.0))),
            Some(1.5)
        );
    }

    #[test]
    fn with_subtitles_off_the_keys_change_nothing() {
        let mut timing = Timing::default();
        assert!(
            timing
                .step(Delay::Subtitles, 1.0, &subtitles_off(0.3))
                .is_empty()
        );
        assert!(
            timing
                .compare(Delay::Subtitles, &subtitles_off(0.3))
                .is_empty()
        );
        assert_eq!(
            timing.hint(Delay::Subtitles, &subtitles_off(0.3), true),
            None
        );
        assert!(!Delay::Subtitles.active(&subtitles_off(0.3)));
        assert!(Delay::Audio.active(&subtitles_off(0.3)));
    }

    #[test]
    fn the_hint_says_what_the_next_select_does() {
        let mut timing = Timing::default();
        let hint = |timing: &Timing, sub: f64, focused: bool| {
            timing.hint(Delay::Subtitles, &film(0.0, sub), focused)
        };
        assert_eq!(hint(&timing, 0.0, true), None);
        assert_eq!(hint(&timing, -0.7, false), None);
        assert_eq!(
            hint(&timing, -0.7, true).as_deref(),
            Some("Select shows the file's timing")
        );
        timing.compare(Delay::Subtitles, &film(0.0, -0.7));
        assert_eq!(
            hint(&timing, 0.0, false).as_deref(),
            Some("Select returns to 0.7 s sooner")
        );
    }
}
