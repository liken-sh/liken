//! What the display does when mpv's IPC socket closes.
//!
//! mpv runs in the playback pod's regular container with restart policy
//! `Never`, so when mpv exits it does not start again, and its socket
//! closes. The display runs in a native sidecar, which the kubelet stops only
//! after the regular container has exited, so the display can keep running
//! after mpv is gone. By then the next screen, such as the media browser's
//! loading overlay, can already show, and anything the display draws would
//! cover it. So once the socket closes, the display draws nothing until its
//! process exits, whatever events arrive later.

use super::Display;

impl Display {
    /// Cancels the hide timers as well, so no timer wakes the frame loop after
    /// mpv is gone.
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

    /// Both layers stop drawing at once, with no fade out.
    #[tokio::test]
    async fn a_detach_draws_nothing_at_once() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        settle(&mut display);
        assert!(
            display.draws(Part::Below),
            "the below layer draws before the detach"
        );
        assert!(
            display.draws(Part::Above),
            "the above layer draws before the detach"
        );

        let _ = display.update(detach());
        assert!(
            !display.draws(Part::Below),
            "the below layer still draws after the detach"
        );
        assert!(
            !display.draws(Part::Above),
            "the above layer still draws after the detach"
        );
        assert_eq!(
            display.logged.last().map(String::as_str),
            Some(
                "media-display: mpv detached, because its socket closed, so the display draws nothing"
            )
        );
    }

    #[tokio::test]
    async fn a_detach_stops_the_fades_and_the_hide_timers() {
        let (mut display, _) = display();
        let _ = display.update(block("next", OFFER));
        let _ = display.update(property("time-pos", json!(5940.0)));
        let _ = display.update(message("summon"));
        assert!(display.fading(), "a fade runs before the detach");
        assert!(
            display.idle.armed.is_some(),
            "the idle timer is armed before the detach"
        );
        assert!(
            display.card.armed.is_some(),
            "the card timer is armed before the detach"
        );

        let _ = display.update(detach());
        assert!(!display.fading(), "a fade still runs after the detach");
        assert_eq!(display.idle.armed, None);
        assert_eq!(display.card.armed, None);
        assert_eq!(display.row.armed, None);
    }

    /// A second detach writes no second log line.
    #[tokio::test]
    async fn nothing_draws_after_a_detach() {
        let (mut display, _) = display();
        let _ = display.update(detach());
        let lines = display.logged.len();

        let _ = display.update(message("summon"));
        let _ = display.update(property("time-pos", json!(1201.0)));
        let _ = display.update(detach());
        assert!(
            !display.draws(Part::Below),
            "the below layer draws after a second detach"
        );
        assert!(
            !display.draws(Part::Above),
            "the above layer draws after a second detach"
        );
        assert!(!display.fading(), "a fade runs after a second detach");
        assert_eq!(display.logged.len(), lines);
    }
}
