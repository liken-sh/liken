//! Moves mpv's subtitles above the controls that the display draws at
//! the bottom of the frame. This code is in `Display` because the
//! controls on screen and the fade's progress are its fields, and
//! `Display::update` runs it after every message. `crate::subtitles`
//! turns the result into mpv's `sub-pos`.

use super::Display;

impl Display {
    /// The topmost canvas row of the controls at the bottom of the frame,
    /// or `None` when the controls are faded out or no subtitle track is
    /// selected. The controls are the time over the scrubber, the up-next
    /// chip or the skip control, the cast row, a scan's thumbnail, and an
    /// open chooser or adjuster.
    fn band_top(&self) -> Option<f32> {
        if !self.focus.fade().shown() || !self.film.tracks_of("sub").any(|track| track.selected) {
            return None;
        }
        [
            Some(crate::scrubber::TOP),
            (self.upnext.chip_showing() || self.skip.available())
                .then_some(crate::upnext::CHIP_TOP),
            self.cast_showing().then_some(crate::cast::TOP),
            self.previewing().map(|_| crate::art::TILE_TOP),
            self.strip.panel_top(&self.film),
        ]
        .into_iter()
        .flatten()
        .reduce(f32::min)
    }

    /// Sends mpv a new `sub-pos` when `band_top` or the fade changed. The
    /// fade sends a message on each tick while it moves, so the subtitles
    /// move with it. Sends nothing once mpv has exited and closed its socket.
    pub(super) fn follow_subtitles(&mut self) {
        if self.detached {
            return;
        }
        let follow = self
            .subtitles
            .follow(self.band_top(), self.focus.fade().value());
        self.say(follow.logged);
        if let Some(command) = follow.command {
            let _ = self.wire.commands.send(crate::ipc::line(&command));
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::super::tests::{OFFER, block, display, message, property, sent, settle};
    use super::super::{Display, Message};
    use crate::subtitles::{REST, lifted};

    /// The film of the shared fixture with an English subtitle track playing
    /// beside its two audio tracks.
    fn subtitled(display: &mut Display) {
        let _ = display.update(property(
            "track-list",
            json!([
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 2, "type": "audio", "lang": "fra" },
                { "id": 1, "type": "sub", "lang": "eng", "selected": true },
            ]),
        ));
    }

    /// The `sub-pos` values the display sent, in order.
    fn positions(lines: &[String]) -> Vec<f64> {
        lines
            .iter()
            .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
            .filter(|sent| sent["command"][1] == "sub-pos")
            .filter_map(|sent| sent["command"][2].as_f64())
            .collect()
    }

    fn rounded(at: f64) -> f64 {
        (at * 10.0).round() / 10.0
    }

    /// Summoning the display raises the subtitles above the time over the
    /// scrubber, one step per fade tick and never downward. Hiding returns
    /// them to their rest position as the display fades out.
    #[tokio::test]
    async fn the_subtitles_rise_and_fall_with_the_display() {
        let (mut display, mut commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        let rise = positions(&sent(&mut commands));
        assert!(
            rise.len() > 2,
            "{rise:?} has 2 or fewer sub-pos steps, want a step per fade tick"
        );
        assert!(
            rise.windows(2).all(|pair| pair[1] < pair[0]),
            "sub-pos while rising is {rise:?}, want each step lower than the one before"
        );
        assert_eq!(rise.last(), Some(&rounded(lifted(crate::scrubber::TOP))));

        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        let fall = positions(&sent(&mut commands));
        assert!(
            fall.len() > 2,
            "{fall:?} has 2 or fewer sub-pos steps, want a step per fade tick"
        );
        assert!(
            fall.windows(2).all(|pair| pair[1] > pair[0]),
            "sub-pos while falling is {fall:?}, want each step higher than the one before"
        );
        assert_eq!(fall.last(), Some(&REST));
    }

    /// With no subtitle track selected, the display sends no `sub-pos`.
    #[tokio::test]
    async fn no_subtitle_track_sends_nothing() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert!(sent(&mut commands).is_empty());
    }

    /// Each case is the key presses after a summon and the row that the
    /// subtitles must clear. With the strip focused, the highest control
    /// is still the scrubber time. The audio chooser's two-row panel is
    /// higher.
    #[tokio::test]
    async fn the_subtitles_clear_the_highest_thing_in_the_band() {
        let cases: [(&[&str], f32); 3] = [
            (&[], crate::scrubber::TOP),
            (&["down", "down"], crate::scrubber::TOP),
            (&["down", "down", "select"], crate::chooser::top(2)),
        ];
        for (presses, top) in cases {
            let (mut display, mut commands) = display();
            subtitled(&mut display);
            let _ = display.update(message("summon"));
            for press in presses {
                let _ = display.update(message(press));
            }
            settle(&mut display);
            assert_eq!(
                positions(&sent(&mut commands)).last(),
                Some(&rounded(lifted(top))),
                "after a summon and the presses {presses:?}, the last sub-pos is not the one above the chooser or adjuster"
            );
        }
    }

    /// Turning the subtitles off while the display is up returns them to
    /// the rest position in one step, so the next track starts there.
    #[tokio::test]
    async fn subtitles_turned_off_return_at_once() {
        let (mut display, mut commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        sent(&mut commands);

        let _ = display.update(property(
            "track-list",
            json!([{ "id": 1, "type": "sub", "lang": "eng" }]),
        ));
        assert_eq!(positions(&sent(&mut commands)), [REST]);
    }

    /// The up-next chip is drawn above the scrubber time, so the subtitles
    /// clear the chip while an offer is showing.
    #[tokio::test]
    async fn the_subtitles_clear_the_up_next_chip() {
        let (mut display, mut commands) = display();
        subtitled(&mut display);
        let _ = display.update(block("next", OFFER));
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert_eq!(
            positions(&sent(&mut commands)).last(),
            Some(&rounded(lifted(crate::upnext::CHIP_TOP)))
        );
    }

    /// The display's record gets one line when the subtitles move up and
    /// one when they return, and none for the ticks in between.
    #[tokio::test]
    async fn the_record_gets_one_line_each_way() {
        let (mut display, _commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        let lines: Vec<&str> = display
            .logged
            .iter()
            .map(String::as_str)
            .filter(|line| line.contains("subtitles"))
            .collect();
        assert_eq!(
            lines,
            [
                "media-display: subtitles move above row 846 of the display, to sub-pos 79.9",
                "media-display: subtitles return to their place, sub-pos 100",
            ]
        );
    }
}
