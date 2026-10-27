//! What the display does when mpv's socket closes.
//!
//! mpv runs in the playback pod's ordinary container, and the pod's restart
//! policy is `Never`, so mpv never starts again inside the same pod. Its
//! socket closes when mpv exits, and the pod ends with it. The display is a
//! native sidecar, which the kubelet stops after mpv's container ends, so the
//! display runs for a few seconds after mpv quits. The next screen, such as
//! the media browser's loading curtain, can show under the display then, so
//! anything the display still draws stands over it. So the detach is final: the
//! display stops drawing and ticking at once, and no later event of the
//! socket brings it back.

use super::Display;

impl Display {
    /// mpv's socket closed. Both layers draw nothing from here on, the fades
    /// stop ticking, and the hide windows are cancelled, so no timer wakes
    /// the frame loop for a screen that is gone.
    pub(super) fn on_detached(&mut self) {
        if self.detached {
            return;
        }
        self.detached = true;
        self.idle.cancel();
        self.card.cancel();
        self.row.cancel();
        self.say(Some(
            "media-display: mpv detached, because its socket closed, so the display draws nothing"
                .to_string(),
        ));
        self.redraw();
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::super::tests::{OFFER, block, display, message, property, settle};
    use super::super::{Message, Part, ipc};

    fn detach() -> Message {
        Message::Mpv(ipc::Event::Detached)
    }

    /// The OSD and the risen card both stand when mpv quits, and both
    /// layers stop drawing at once, with no fade out.
    #[tokio::test]
    async fn a_detach_draws_nothing_at_once() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert!(display.draws(Part::Below));
        assert!(display.draws(Part::Above));

        let _ = display.update(detach());
        assert!(!display.draws(Part::Below));
        assert!(!display.draws(Part::Above));
        assert_eq!(
            display.said.last().map(String::as_str),
            Some(
                "media-display: mpv detached, because its socket closed, so the display draws nothing"
            )
        );
    }

    /// A fade in flight stops ticking, and every hide window is cancelled.
    #[tokio::test]
    async fn a_detach_stops_the_ticks_and_the_windows() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        assert!(display.fading());
        assert!(display.idle.armed.is_some());
        assert!(display.card.armed.is_some());

        let _ = display.update(detach());
        assert!(!display.fading());
        assert_eq!(display.idle.armed, None);
        assert_eq!(display.card.armed, None);
        assert_eq!(display.row.armed, None);
    }

    /// The detach is final: a press or a position that still arrives draws
    /// nothing, and a second detach writes no second line.
    #[tokio::test]
    async fn nothing_draws_after_a_detach() {
        let (mut display, _) = display();
        let _ = display.update(detach());
        let lines = display.said.len();

        let _ = display.update(message("summon"));
        let _ = display.update(property("time-pos", json!(1201.0)));
        let _ = display.update(detach());
        assert!(!display.draws(Part::Below));
        assert!(!display.draws(Part::Above));
        assert!(!display.fading());
        assert_eq!(display.said.len(), lines);
    }
}
