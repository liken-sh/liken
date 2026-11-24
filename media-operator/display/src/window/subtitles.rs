//! Places the selected subtitle track and moves its cues above the
//! controls that the display draws at the bottom of the frame. This code
//! is in `Display` because the film, the presentation, the controls on
//! screen, and the fade's progress are its fields, and `Display::update`
//! runs it after every message. `crate::subtitles` turns them into mpv's
//! properties.

use super::Display;
use crate::subtitles::{measurement, placement};

impl Display {
    /// The topmost canvas row of the controls at the bottom of the frame,
    /// or `None` when the controls are faded out or no subtitle track is
    /// selected. The controls are the time over the scrubber, the up-next
    /// chip or the skip control, the cast row, a scan's thumbnail, and an
    /// open track panel.
    fn band_top(&self) -> Option<f32> {
        if !self.focus.fade().shown() || self.film.selected_subtitle().is_none() {
            return None;
        }
        [
            Some(crate::scrubber::TOP),
            (self.upnext.chip_showing() || self.skip.available())
                .then_some(crate::upnext::CHIP_TOP),
            self.cast_showing().then_some(crate::cast::TOP),
            self.previewing().map(|_| crate::art::TILE_TOP),
            self.strip.panel_top(&self.presentation, &self.film),
        ]
        .into_iter()
        .flatten()
        .reduce(f32::min)
    }

    /// Sends mpv each placing property whose value changed: when the
    /// selected track, its header, the screen, the item's presentation,
    /// `band_top`, or the fade changed. The fade sends a message on each
    /// tick while it moves, so the subtitles move with it. Sends nothing
    /// once mpv has exited and closed its socket.
    pub(super) fn follow_subtitles(&mut self) {
        if self.detached {
            return;
        }
        if self
            .subtitles
            .reads_script(self.film.path.as_deref(), self.film.selected_subtitle())
        {
            self.film.forget_script();
            for line in crate::ipc::observe_script() {
                let _ = self.wire.commands.send(line);
            }
        }
        let top = self.band_top();
        let film = &self.film;
        let placed = film.selected_subtitle().and_then(|track| {
            let measured = measurement::find(self.presentation.measured(film), track);
            placement::place(track, film.script.as_ref(), film.screen.as_ref(), measured)
                .map(|placement| (track, placement))
        });
        let follow = self
            .subtitles
            .follow(placed, top, self.focus.fade().value());
        for line in follow.logged {
            self.say(Some(line));
        }
        for command in follow.commands {
            let _ = self.wire.commands.send(crate::ipc::line(&command));
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::{Value, json};
    use tokio::sync::broadcast;

    use super::super::tests::{OFFER, block, display, message, property, sent, settle};
    use super::super::{Display, Message};
    use crate::subtitles::lift::lifted;
    use crate::subtitles::placement::Lift;

    const REST: f64 = 100.0;

    /// The film of the shared fixture with an English subtitle track playing
    /// beside its two audio tracks.
    fn subtitled(display: &mut Display) {
        let _ = display.update(property(
            "track-list",
            json!([
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 2, "type": "audio", "lang": "fra" },
                { "id": 1, "type": "sub", "lang": "eng", "codec": "subrip", "selected": true },
            ]),
        ));
    }

    /// The values the display sent for the property `name`, in order.
    fn values(lines: &[String], name: &str) -> Vec<Value> {
        lines
            .iter()
            .filter_map(|line| serde_json::from_str::<Value>(line).ok())
            .filter(|sent| sent["command"][0] == "set_property" && sent["command"][1] == name)
            .map(|sent| sent["command"][2].clone())
            .collect()
    }

    fn positions(lines: &[String]) -> Vec<f64> {
        values(lines, "sub-pos")
            .iter()
            .filter_map(Value::as_f64)
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
        assert_eq!(
            rise.last(),
            Some(&rounded(lifted(REST, Lift::Share, crate::scrubber::TOP)))
        );

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

    /// With no subtitle track selected, the display sends nothing.
    #[tokio::test]
    async fn no_subtitle_track_sends_nothing() {
        let (mut display, mut commands) = display();
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert!(sent(&mut commands).is_empty());
    }

    /// The top of the open track panel, which is above the scrubber time.
    fn panel_top(display: &Display) -> f32 {
        let top = display
            .strip
            .panel_top(&display.presentation, &display.film)
            .expect("an open track panel");
        assert!(
            top < crate::scrubber::TOP,
            "the panel's top is at {top}, below the scrubber time"
        );
        top
    }

    /// The presses after a summon, and the row the subtitles then clear.
    type Clears = (&'static [&'static str], fn(&Display) -> f32);

    /// Each case is the key presses after a summon and the row that the
    /// subtitles must clear. With the strip focused, the highest control
    /// is still the scrubber time. The audio panel is higher.
    #[tokio::test]
    async fn the_subtitles_clear_the_highest_thing_in_the_band() {
        let scrubber = |_: &Display| crate::scrubber::TOP;
        let cases: [Clears; 3] = [
            (&[], scrubber),
            (&["down", "down"], scrubber),
            (&["down", "down", "select"], panel_top),
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
                Some(&rounded(lifted(REST, Lift::Share, top(&display)))),
                "after a summon and the presses {presses:?}, the last sub-pos is not the one above the highest control"
            );
        }
    }

    /// Turning the subtitles off while the display is up leaves every
    /// property as it is, because no track draws. The next track that is
    /// selected gets its own values.
    #[tokio::test]
    async fn subtitles_turned_off_send_nothing_until_a_track_is_selected() {
        let (mut display, mut commands) = display();
        subtitled(&mut display);
        let _ = display.update(message("summon"));
        settle(&mut display);
        sent(&mut commands);

        let _ = display.update(property(
            "track-list",
            json!([{ "id": 1, "type": "sub", "lang": "eng", "codec": "subrip" }]),
        ));
        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        assert_eq!(sent(&mut commands), Vec::<String>::new());

        subtitled(&mut display);
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
            Some(&rounded(lifted(REST, Lift::Share, crate::upnext::CHIP_TOP)))
        );
    }

    /// The display's record gets one line for the track, one when the
    /// subtitles move up, and one when they return, and none for the ticks
    /// in between.
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
            .filter(|line| line.contains("subtitle"))
            .collect();
        assert_eq!(
            lines,
            [
                "media-display: subtitle track 1, codec \"subrip\", ends on the house row, with sub-ass-override scale, stretch-image-subs-to-screen no, sub-scale 1, and sub-pos 100",
                "media-display: subtitles move above row 870 of the display, to sub-pos 82.2",
                "media-display: subtitles return to their place, sub-pos 100",
            ]
        );
    }

    /// The inputs that place a track, each as the message that delivers
    /// it. mpv and the command sidecar send them separately.
    #[derive(Debug, Clone, Copy)]
    enum Input {
        PgsTrack,
        AssTrack,
        Screen,
        Presentation,
        Header,
    }

    fn deliver(display: &mut Display, input: Input) {
        let message = match input {
            Input::PgsTrack => property(
                "track-list",
                json!([
                    { "id": 1, "type": "video", "selected": true },
                    { "id": 1, "type": "sub", "codec": "subrip", "ff-index": 2 },
                    { "id": 2, "type": "sub", "codec": "hdmv_pgs_subtitle", "ff-index": 3, "selected": true },
                ]),
            ),
            Input::AssTrack => property(
                "track-list",
                json!([
                    { "id": 1, "type": "video", "selected": true },
                    { "id": 1, "type": "sub", "codec": "ass", "ff-index": 2, "selected": true },
                ]),
            ),
            Input::Screen => property(
                "osd-dimensions",
                json!({ "w": 1920, "h": 1080, "par": 1.0, "aspect": 1.7778, "mt": 138, "mb": 138, "ml": 0, "mr": 0 }),
            ),
            Input::Presentation => block(
                "presentation",
                r#"{"title":"A Film","subtitles":[{"stream":3,"events":1382,
                    "image":{"canvasHeight":1080,"lineHeight":61,"inkBottom":918}}]}"#,
            ),
            Input::Header => property(
                "sub-ass-extradata",
                json!(
                    "[Script Info]\nPlayResY: 804\n\n[V4+ Styles]\nFormat: Name, Alignment, MarginV\nStyle: Default, 2, 20\n"
                ),
            ),
        };
        let _ = display.update(message);
    }

    /// Every property that the display sent, with each value in order.
    fn placing(commands: &mut broadcast::Receiver<String>) -> Vec<(&'static str, Vec<Value>)> {
        let lines = sent(commands);
        [
            "sub-ass-override",
            "stretch-image-subs-to-screen",
            "sub-scale",
            "sub-pos",
        ]
        .into_iter()
        .map(|name| (name, values(&lines, name)))
        .filter(|(_, values)| !values.is_empty())
        .collect()
    }

    /// mpv and the sidecar send the track, the screen, and the item's
    /// presentation in any order. The display sends each value once,
    /// when the last input arrives, and no value before it.
    #[tokio::test]
    async fn a_measured_pgs_track_is_placed_once_whatever_order_its_inputs_arrive_in() {
        use Input::{PgsTrack, Presentation, Screen};
        let orders = [
            [PgsTrack, Screen, Presentation],
            [PgsTrack, Presentation, Screen],
            [Screen, PgsTrack, Presentation],
            [Screen, Presentation, PgsTrack],
            [Presentation, PgsTrack, Screen],
            [Presentation, Screen, PgsTrack],
        ];
        for order in orders {
            let (mut display, mut commands) = display();
            for input in order {
                deliver(&mut display, input);
            }
            assert_eq!(
                placing(&mut commands),
                [
                    ("stretch-image-subs-to-screen", vec![json!(true)]),
                    ("sub-scale", vec![json!(0.7541)]),
                    ("sub-pos", vec![json!(111.157)]),
                ],
                "after the inputs in the order {order:?}"
            );
        }
    }

    /// The header is requested only once the ASS track is selected, so it
    /// arrives after the track. The screen can arrive at any time.
    #[tokio::test]
    async fn an_ass_track_is_placed_once_its_header_and_the_screen_arrive() {
        use Input::{AssTrack, Header, Screen};
        let orders = [
            [AssTrack, Screen, Header],
            [AssTrack, Header, Screen],
            [Screen, AssTrack, Header],
        ];
        for order in orders {
            let (mut display, mut commands) = display();
            for input in order {
                deliver(&mut display, input);
            }
            assert_eq!(
                placing(&mut commands),
                [
                    ("sub-ass-override", vec![json!("force")]),
                    ("sub-pos", vec![json!(97.358)]),
                ],
                "after the inputs in the order {order:?}"
            );
        }
    }

    /// Selecting an ASS track makes mpv send its header, and a header that
    /// arrived for an earlier track is not used for it.
    #[tokio::test]
    async fn selecting_an_ass_track_observes_its_header_again() {
        let (mut display, mut commands) = display();
        deliver(&mut display, Input::Screen);
        deliver(&mut display, Input::Header);
        sent(&mut commands);

        deliver(&mut display, Input::AssTrack);
        assert_eq!(
            sent(&mut commands),
            crate::ipc::observe_script().map(|line| line.trim_end().to_string())
        );
        assert_eq!(display.film.script, None);
    }

    /// The lift of a measured image track starts from its resting value,
    /// and hiding returns it there, not to 100.
    #[tokio::test]
    async fn an_image_track_rises_from_its_resting_value_and_returns_to_it() {
        let (mut display, mut commands) = display();
        for input in [Input::Screen, Input::Presentation, Input::PgsTrack] {
            deliver(&mut display, input);
        }
        sent(&mut commands);
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert_eq!(
            positions(&sent(&mut commands)).last(),
            Some(&rounded(lifted(111.157, Lift::Rows, crate::scrubber::TOP)))
        );
        let armed = display.idle.armed.expect("a summon arms the idle window");
        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        assert_eq!(positions(&sent(&mut commands)).last(), Some(&111.157));
    }
}
