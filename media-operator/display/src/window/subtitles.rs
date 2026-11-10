//! Where the display asks mpv to draw the subtitles. The frame loop is the
//! one place that knows what the display draws in the band at the foot of
//! the frame and how far its fade has moved, so it decides after every
//! message, and `crate::subtitles` turns that into mpv's `sub-pos`.

use super::Display;

impl Display {
    /// The highest canvas row the display draws on in the band at the foot of
    /// the frame, or nothing while the display draws nothing there or no
    /// subtitle track plays. It follows what `paint_below` and `paint_above`
    /// draw: the time over the scrubber always, the up-next chip or the skip
    /// control on the row above it, the cast row, a scan's thumbnail, and an
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

    /// Send mpv the subtitles' position for the band and the fade as they
    /// stand after one message. The fade ticks only while it moves, so the
    /// subtitles glide with it, and a message that changes neither sends
    /// nothing.
    pub(super) fn follow_subtitles(&mut self) {
        if self.detached {
            return;
        }
        let follow = self
            .subtitles
            .follow(self.band_top(), self.focus.fade().value());
        self.say(follow.said);
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

    /// A summon lifts the subtitles above the time over the scrubber as the
    /// display fades in, one step for each tick and never back down, and
    /// the hide returns them to their place as it fades out.
    #[tokio::test]
    async fn the_subtitles_rise_and_fall_with_the_display() {
        let (mut display, mut commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        let rise = positions(&sent(&mut commands));
        assert!(rise.len() > 2, "{rise:?}");
        assert!(rise.windows(2).all(|pair| pair[1] < pair[0]), "{rise:?}");
        assert_eq!(rise.last(), Some(&rounded(lifted(crate::scrubber::TOP))));

        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        let fall = positions(&sent(&mut commands));
        assert!(fall.len() > 2, "{fall:?}");
        assert!(fall.windows(2).all(|pair| pair[1] > pair[0]), "{fall:?}");
        assert_eq!(fall.last(), Some(&REST));
    }

    /// A film with no subtitle track playing sends mpv nothing for them.
    #[tokio::test]
    async fn no_subtitle_track_sends_nothing() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert!(sent(&mut commands).is_empty());
    }

    /// Each case is the presses after a summon and the row the subtitles
    /// clear: the strip in focus leaves them over the scrubber, and the
    /// audio chooser's panel of two rows stands higher.
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
                "{presses:?}"
            );
        }
    }

    /// Subtitles turned off while the display is up return to their place
    /// in one step, so the next track a person turns on starts there.
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

    /// The up-next chip stands on the row above the time, so the subtitles
    /// clear the chip while the offer waits.
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

    /// The record takes one line as the subtitles move up and one as they
    /// return, and none for the ticks between.
    #[tokio::test]
    async fn the_record_takes_one_line_each_way() {
        let (mut display, _commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        let lines: Vec<&str> = display
            .said
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
