//! The display's handlers for stepping the film back. When the up-next card
//! rises near the end of a film, the display asks the compositor to shrink
//! the film into a frame on the left, so the offer can draw as a column
//! beside it. The handlers pass the compositor's events, the rise, key
//! presses, and the window's close to `crate::arranger`, which decides
//! each arrangement. Then they send that arrangement through the link to
//! the compositor and update the up-next offer.

use iced::Task;

use super::{Display, Message};
use crate::arranger::link::Link;
use crate::arranger::{Change, Event};

impl Display {
    /// Called when the link on winit's Wayland connection has started, or
    /// failed to start. Without a link, the film never steps back.
    pub(super) fn on_attached(&mut self, link: Result<Link, String>) {
        match link {
            Ok(link) => self.link = Some(link),
            Err(error) => self.say(Some(format!(
                "media-display: the arranger link did not start, so the film does not shrink for the up-next column: {error}"
            ))),
        }
    }

    /// Handle one event from the compositor. When the film has finished
    /// shrinking, the column fades in. When the compositor returned the film
    /// to the full region without a request, the offer returns to the corner
    /// card.
    pub(super) fn on_arranger(&mut self, event: Event) -> Task<Message> {
        let change = self.arranger.on_event(event);
        self.apply(change);
        if self.arranger.landed() && self.upnext.column().value() == 0.0 {
            self.upnext.landed();
        }
        if self.upnext.stepped_back() && !self.arranger.stepped_back() {
            self.upnext.fall_back();
            self.redraw();
            return self.arm_hide_timers();
        }
        Task::none()
    }

    /// The up-next card rose. A film or an episode steps back, and the offer
    /// draws as a column beside it once the film has finished shrinking.
    /// Music and pictures keep the corner card, because the display draws
    /// over the whole frame for them.
    pub(super) fn on_rise(&mut self) {
        if self.presentation.is_music() || self.presentation.is_image() {
            return;
        }
        let change = self
            .arranger
            .step_back("the up-next card rose near the end of the work");
        if change.commit.is_none() {
            return;
        }
        self.apply(change);
        if self.arranger.stepped_back() {
            self.upnext.step_back();
        }
    }

    /// Called after a key press. When the press took the offer, the film
    /// fades out in its frame along with the column. When back turned the
    /// column into the corner card, the film returns to the full region.
    pub(super) fn after_press(&mut self, back: bool, was_stepped: bool, was_waiting: bool) {
        if !was_waiting && self.upnext.waiting() {
            let change = self.arranger.hand_off("the up-next offer was taken");
            self.apply(change);
            return;
        }
        if !(back && was_stepped && !self.upnext.stepped_back()) {
            return;
        }
        let change = self
            .arranger
            .restore("back was pressed over the stepped-back film");
        self.apply(change);
    }

    /// Called after every message. When the offer has gone away while the
    /// film is stepped back, such as when a new block replaced it or the
    /// Play moved to another item, the film returns to the full region.
    pub(super) fn follow_offer(&mut self) {
        if !self.arranger.stepped_back() || self.upnext.stepped_back() {
            return;
        }
        let change = self.arranger.restore("the up-next offer left");
        self.apply(change);
    }

    /// The compositor asked the window to close. The link closes before iced
    /// exits, so the link's thread ends before winit disconnects the Wayland
    /// connection that the thread reads.
    pub(super) fn on_close(&mut self) -> Task<Message> {
        self.copies.remove_files();
        if let Some(link) = self.link.take() {
            let line = match link.close() {
                true => "media-display: arranger destroyed, because the window closes",
                false => {
                    "media-display: arranger destroyed, because the window closes, and the compositor did not answer in time"
                }
            };
            self.say(Some(line.to_string()));
        }
        iced::exit()
    }

    /// Log the change and send its arrangement. When the send fails, the
    /// arranger receives `Event::Lost`, as if the compositor had reported
    /// the broken connection.
    fn apply(&mut self, change: Change) {
        self.say(change.line);
        let Some(commit) = change.commit else {
            return;
        };
        #[cfg(test)]
        self.committed.push(commit.clone());
        let Some(link) = self.link.as_ref() else {
            return;
        };
        if let Err(error) = link.commit(&commit) {
            let lost = self.arranger.on_event(Event::Lost(error));
            self.say(lost.line);
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::super::tests::{OFFER, TAKE, block, display, message, sent, settle};
    use super::*;
    use crate::arranger::{VIDEO, handing_off, restored};
    use crate::upnext;

    /// The events of a compositor that granted the claim, with a 1920 by 1080
    /// region that shows mpv's window.
    fn grant(display: &mut Display) {
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
            let _ = display.update(Message::Arranger(event));
        }
    }

    /// The film near its end, with the up-next card risen.
    fn rise(display: &mut Display) {
        let _ = display.update(block("next", OFFER));
        let _ = display.update(super::super::tests::property("time-pos", json!(5940.0)));
    }

    #[tokio::test]
    async fn the_rise_steps_the_film_back_when_the_compositor_granted() {
        let (mut display, _) = display();
        grant(&mut display);
        rise(&mut display);

        let names: Vec<&str> = display
            .committed
            .iter()
            .map(|commit| commit.arrangement.name)
            .collect();
        assert_eq!(names, ["stepped-back"]);
        assert!(display.upnext.stepped_back());
        assert_eq!(display.card.armed, None, "the column has no hide window");
        assert!(!display.fading(), "nothing ticks while the film moves");
    }

    /// Without an arranger, the display keeps the corner card and its own
    /// hide timeout.
    #[tokio::test]
    async fn the_rise_keeps_the_corner_card_with_no_arranger() {
        for refusal in [
            None,
            Some(Event::Absent),
            Some(Event::Refused(crate::arranger::Refusal::Busy)),
        ] {
            let (mut display, _) = display();
            if let Some(event) = refusal.clone() {
                let _ = display.update(Message::Arranger(event));
            }
            rise(&mut display);
            settle(&mut display);
            assert!(display.committed.is_empty(), "{refusal:?}");
            assert!(!display.upnext.stepped_back(), "{refusal:?}");
            assert!(display.upnext.draws_outside(false), "{refusal:?}");
            assert!(display.card.armed.is_some(), "{refusal:?}");
        }
    }

    #[tokio::test]
    async fn a_music_item_keeps_the_corner_card() {
        let (mut display, _) = display();
        let _ = display.update(block("presentation", r#"{"type":"music"}"#));
        grant(&mut display);
        rise(&mut display);
        assert!(display.committed.is_empty());
        assert!(!display.upnext.stepped_back());
    }

    #[tokio::test]
    async fn the_landing_fades_the_column_in() {
        let (mut display, _) = display();
        grant(&mut display);
        rise(&mut display);
        assert!(!display.upnext.draws_outside(false));

        let _ = display.update(Message::Arranger(Event::Landed(1)));
        assert!(display.fading());
        settle(&mut display);
        assert!(display.upnext.draws_outside(false));
        assert!(display.upnext.draws_outside(true));
    }

    /// The first back returns the film and keeps playing, and the second
    /// sends `liken-exit` to mpv.
    #[tokio::test]
    async fn back_returns_the_film_and_a_second_back_ends_the_run() {
        let (mut display, mut commands) = display();
        grant(&mut display);
        rise(&mut display);
        let _ = display.update(Message::Arranger(Event::Landed(1)));
        settle(&mut display);

        let _ = display.update(message("back"));
        assert!(sent(&mut commands).is_empty());
        assert_eq!(
            display.committed.last().map(|commit| &commit.arrangement),
            Some(&restored())
        );
        assert!(!display.upnext.stepped_back());
        assert!(
            display
                .logged
                .iter()
                .any(|line| line.ends_with("because back was pressed over the stepped-back film"))
        );

        let _ = display.update(message("back"));
        assert_eq!(
            sent(&mut commands),
            vec!["{\"command\":[\"script-message\",\"liken-exit\"],\"request_id\":0}"]
        );
    }

    /// The film stepped back and finished shrinking, with the column showing.
    fn column(display: &mut Display) {
        grant(display);
        rise(display);
        let _ = display.update(Message::Arranger(Event::Landed(1)));
        settle(display);
    }

    /// A select on the column starts the next work, and in the same press
    /// the film fades out in its frame while the column fades out.
    #[tokio::test]
    async fn a_select_on_the_column_fades_the_film_and_the_column_out() {
        let (mut display, mut commands) = display();
        column(&mut display);

        let _ = display.update(message("select"));
        assert_eq!(sent(&mut commands), TAKE);
        assert_eq!(
            display.committed.last().map(|commit| &commit.arrangement),
            Some(&handing_off(1920, 1080))
        );
        assert_eq!(display.committed.len(), 2);
        assert!(
            display
                .logged
                .iter()
                .any(|line| line.ends_with("because the up-next offer was taken"))
        );
        assert!(display.fading(), "the column fades out");
        settle(&mut display);
        assert!(!display.upnext.draws_outside(false));
        assert!(!display.upnext.draws_outside(true));

        // A `Landed` event after the fade does not bring the column back.
        let _ = display.update(Message::Arranger(Event::Landed(2)));
        settle(&mut display);
        assert!(!display.upnext.draws_outside(false));
    }

    /// When the connection fails during the hand-off, the compositor returns
    /// the film, and the corner card does not draw over it.
    #[tokio::test]
    async fn a_lost_link_during_the_hand_off_brings_no_card_back() {
        let (mut display, mut commands) = display();
        column(&mut display);
        let _ = display.update(message("select"));
        let _ = sent(&mut commands);

        let _ = display.update(Message::Arranger(Event::Lost("Broken pipe".to_string())));
        settle(&mut display);
        assert!(!display.upnext.stepped_back());
        assert!(!display.upnext.draws_outside(false));
        assert_eq!(display.card.armed, None);
        assert_eq!(display.committed.len(), 2, "no restore follows");
    }

    /// When the offer goes away, the film returns to the whole region.
    #[tokio::test]
    async fn an_offer_that_leaves_returns_the_film() {
        let (mut display, _) = display();
        grant(&mut display);
        rise(&mut display);
        let _ = display.update(block("next", "{}"));
        assert_eq!(
            display.committed.last().map(|commit| &commit.arrangement),
            Some(&restored())
        );
    }

    /// When the connection fails while the film is stepped back, the
    /// compositor returns the film, and the offer falls back to the corner
    /// card.
    #[tokio::test]
    async fn a_lost_link_falls_back_to_the_corner_card() {
        let (mut display, _) = display();
        grant(&mut display);
        rise(&mut display);
        let _ = display.update(Message::Arranger(Event::Lost("Broken pipe".to_string())));
        settle(&mut display);
        assert!(!display.upnext.stepped_back());
        assert!(display.upnext.draws_outside(false));
        assert!(display.card.armed.is_some());
    }

    /// The column's image is decoded at the size of the column.
    #[tokio::test]
    async fn the_column_decodes_its_art_at_the_column_size() {
        let (mut display, _) = display();
        grant(&mut display);
        let _ = display.update(block(
            "next",
            super::super::tests::offer_with_art().as_str(),
        ));
        let _ = display.update(super::super::tests::property("time-pos", json!(5940.0)));
        assert_eq!(display.upnext.art_box(), iced::Size::new(704.0, 396.0));
        assert_eq!(display.upnext.left(&display.canvas), 1120.0);
        assert_ne!(
            display.upnext.left(&display.canvas),
            upnext::card_x(&display.canvas)
        );
    }

    #[tokio::test]
    async fn a_failed_bind_says_why() {
        let (mut display, _) = display();
        let _ = display.update(Message::Attached(Err(
            "the window is not on Wayland".to_string()
        )));
        assert_eq!(
            display.logged.last().map(String::as_str),
            Some(
                "media-display: the arranger link did not start, so the film does not shrink for the up-next column: the window is not on Wayland"
            )
        );
    }
}
