//! The browser's connection to the broker, and the decoding of what arrives
//! on it.
//!
//! The browser holds no keymap, no focus mark, and no shade of its own.
//! `media-operator`'s idle command pod holds all three, and this client reads
//! two topics: the presses that pod forwards, and the moments it decides. The
//! reader runs on a thread of its own and delivers into a channel the browser
//! drains on every wake of its loop, so a broker that answers slowly never
//! delays a frame.

pub mod press;
pub mod reader;
pub mod screen;

use crate::harness::Waker;

/// What the browser needs from the bus. It is a trait so the browser's tests
/// fold real messages and see a real request with no socket under them.
pub trait Bus {
    /// Every message that arrived since the last call. The call never blocks.
    fn drain(&self) -> Vec<Message>;

    /// Ask the idle command pod to bring the shade down. It is the one request
    /// a client makes, from its top level with nowhere left to go back to.
    fn request_sleep(&self);

    /// Wake the loop on every delivery, so a press shows on the next
    /// frame rather than at the next scheduled second.
    fn wake_on_delivery(&self, wake: Waker);
}

/// One message the browser read off the bus.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Message {
    /// One navigation press, as the browser key it is.
    Press(&'static str),
    /// One moment the idle command pod decided.
    Screen(screen::Event),
}

/// The decoding of one bus session: which topic carries what. No socket
/// reaches this type, so a test proves the rules below without a broker.
#[derive(Debug)]
pub struct Session {
    commands: String,
    screen: String,
}

impl Session {
    pub fn new(commands: String, screen: String) -> Self {
        Self { commands, screen }
    }

    /// The topics to subscribe to. The commands topic carries the browser's
    /// own sleep request back to it, and `press::parse` names no key for that
    /// action, so the browser reads its own publish as nothing.
    pub fn filters(&self) -> Vec<String> {
        vec![self.commands.clone(), self.screen.clone()]
    }

    /// One inbound message, decoded. A topic this session did not subscribe
    /// to, and a payload that does not decode, are both nothing at all.
    pub fn deliver(&self, topic: &str, payload: &[u8]) -> Option<Message> {
        if topic == self.commands {
            return press::parse(payload).map(Message::Press);
        }
        if topic == self.screen {
            return screen::parse(payload).map(Message::Screen);
        }
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const COMMANDS: &str = "liken/media/players/house/den-tv/commands";
    const SCREEN: &str = "liken/media/players/house/den-tv/screen";

    fn session() -> Session {
        Session::new(COMMANDS.into(), SCREEN.into())
    }

    #[test]
    fn the_session_subscribes_to_the_two_topics_the_operator_named() {
        assert_eq!(session().filters(), [COMMANDS, SCREEN]);
    }

    #[test]
    fn a_press_decodes_off_the_commands_topic() {
        assert_eq!(
            session().deliver(COMMANDS, br#"{"action":"select"}"#),
            Some(Message::Press("enter"))
        );
    }

    #[test]
    fn a_moment_decodes_off_the_screen_topic() {
        assert_eq!(
            session().deliver(SCREEN, br#"{"event":"present"}"#),
            Some(Message::Screen(screen::Event::Present))
        );
    }

    #[test]
    fn the_browsers_own_sleep_request_comes_back_as_nothing() {
        assert_eq!(session().deliver(COMMANDS, br#"{"action":"sleep"}"#), None);
    }

    #[test]
    fn a_topic_this_session_did_not_subscribe_to_is_nothing() {
        assert_eq!(
            session().deliver("liken/media/players/house/den-tv/status", b"{}"),
            None
        );
    }

    #[test]
    fn text_that_does_not_parse_changes_nothing() {
        assert_eq!(session().deliver(COMMANDS, b"{"), None);
        assert_eq!(session().deliver(SCREEN, b"{"), None);
    }
}
