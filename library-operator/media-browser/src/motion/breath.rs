// The breath of the focus outline. While focus rests, the outline
// brightens from the mark's color to `look::breath_peak` and back, slowly and
// at long intervals, so a person who looks up from the couch finds focus
// without a press. Only the color changes, so nothing moves under the
// eye. A press releases the breath at once, from whatever brightness the
// outline had.
//
// The breath also holds the second the last motion ends, because the
// rest is measured from it, and because the frames the loop asks for come
// from the same timeline: every frame of a motion, every frame of a breath
// and of its release, and between breaths only the second the next one
// starts.

use std::f64::consts::TAU;

use crate::look;

/// When focus came to rest, and the release of the breath the last press
/// interrupted.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Breath {
    // The second the last press's motion ends. The rest starts here.
    quiet: f64,
    // The brightness the outline had when the last press arrived, and that
    // second, so the release runs down from there.
    release: Option<(f32, f64)>,
}

impl Breath {
    /// Take a press at this second whose motion ends at `ends`.
    pub fn press(&mut self, at: f64, ends: f64) {
        let level = self.level(at);
        self.release = (level > 0.0).then_some((level, at));
        self.quiet = ends.max(at);
    }

    /// Whether a motion a press started still runs at this second.
    pub fn moving(&self, at: f64) -> bool {
        at < self.quiet
    }

    /// How bright the outline is at this second, from 0 at the mark's color
    /// to 1 at the breath's peak.
    pub fn level(&self, at: f64) -> f32 {
        self.cycle(at).max(self.released(at))
    }

    // The breath alone: a raised cosine over `BREATH`, once every
    // `BREATH_EVERY`, from `BREATH_DELAY` after the rest started.
    fn cycle(&self, at: f64) -> f32 {
        let Some(phase) = self.phase(at) else {
            return 0.0;
        };
        if phase >= look::BREATH {
            return 0.0;
        }
        (0.5 - 0.5 * (TAU * phase / look::BREATH).cos()) as f32
    }

    // How far into the current period of breaths this second is, or
    // nothing before the first breath.
    fn phase(&self, at: f64) -> Option<f64> {
        let since = at - self.first();
        (since >= 0.0).then(|| since % look::BREATH_EVERY)
    }

    // The second the first breath of this rest starts.
    fn first(&self) -> f64 {
        self.quiet + look::BREATH_DELAY
    }

    // What is left of the breath the last press released.
    fn released(&self, at: f64) -> f32 {
        let Some((level, from)) = self.release else {
            return 0.0;
        };
        level * (1.0 - look::eased(look::share(at - from, look::BREATH_RELEASE)))
    }

    /// The second the outline next changes: now while a motion, a release,
    /// or a breath runs, and the start of the next breath between them.
    /// `breathes` is whether an outline is on the screen to breathe; where
    /// none is, only a motion asks for frames.
    pub fn next_frame(&self, at: f64, breathes: bool) -> Option<f64> {
        if self.moving(at) {
            return Some(at);
        }
        if !breathes {
            return None;
        }
        if self.released(at) > 0.0 {
            return Some(at);
        }
        match self.phase(at) {
            None => Some(self.first()),
            Some(phase) if phase < look::BREATH => Some(at),
            // The next start is counted in whole periods from the first, so
            // the second it names is the one the breath starts on.
            Some(_) => {
                let periods = ((at - self.first()) / look::BREATH_EVERY).floor() + 1.0;
                Some(self.first() + periods * look::BREATH_EVERY)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The second the last press's motion ends in these cases.
    const QUIET: f64 = 10.0;

    fn rested() -> Breath {
        let mut breath = Breath::default();
        breath.press(QUIET - look::FOLLOW, QUIET);
        breath
    }

    // The second the first breath after the rest starts.
    const FIRST: f64 = QUIET + look::BREATH_DELAY;

    #[test]
    fn the_outline_is_still_until_the_rest_has_lasted_the_delay() {
        let breath = rested();
        assert_eq!(breath.level(QUIET), 0.0);
        assert_eq!(breath.level(FIRST - 0.01), 0.0);
        assert_eq!(breath.level(FIRST), 0.0);
    }

    #[test]
    fn a_breath_peaks_at_the_full_accent_halfway_and_ends_at_the_mark() {
        let breath = rested();
        let cases = [
            (0.0, 0.0),
            (look::BREATH / 2.0, 1.0),
            (look::BREATH, 0.0),
            (look::BREATH + 1.0, 0.0),
            (look::BREATH_EVERY + look::BREATH / 2.0, 1.0),
        ];
        for (since, level) in cases {
            let drawn = breath.level(FIRST + since);
            assert!((drawn - level).abs() < 1e-6, "{since}: {drawn}");
        }
    }

    #[test]
    fn a_press_releases_the_breath_from_the_brightness_drawn_then() {
        let mut breath = rested();
        let peak = FIRST + look::BREATH / 2.0;
        breath.press(peak, peak + look::FOLLOW);
        let cases = [
            (0.0, 1.0),
            (look::BREATH_RELEASE / 2.0, 0.5),
            (look::BREATH_RELEASE, 0.0),
        ];
        for (since, level) in cases {
            let drawn = breath.level(peak + since);
            assert!((drawn - level).abs() < 1e-6, "{since}: {drawn}");
        }
    }

    #[test]
    fn the_frames_follow_the_motion_the_release_and_the_breath() {
        let mut released = rested();
        let peak = FIRST + look::BREATH / 2.0;
        released.press(peak, peak);
        let cases = [
            (rested(), QUIET - 0.1, true, Some(QUIET - 0.1)),
            (rested(), QUIET - 0.1, false, Some(QUIET - 0.1)),
            (rested(), QUIET, true, Some(FIRST)),
            (rested(), QUIET, false, None),
            (rested(), FIRST + 1.0, true, Some(FIRST + 1.0)),
            (
                rested(),
                FIRST + look::BREATH,
                true,
                Some(FIRST + look::BREATH_EVERY),
            ),
            (
                released,
                peak + look::BREATH_RELEASE / 2.0,
                true,
                Some(peak + look::BREATH_RELEASE / 2.0),
            ),
        ];
        for (breath, at, breathes, next) in cases {
            assert_eq!(breath.next_frame(at, breathes), next, "{at}");
        }
    }
}
