//! The browser's own half of the bus. The `media-screen` crate holds
//! the connection, the key names, the focus gate, and the shade. What
//! is here is the browser's own words on the wire: the play
//! request it publishes with a body of its own, the offer block it
//! writes onto that request and reads back when a person takes the
//! offer, the answer to who is watching, and the mark a person sets on a
//! title.

pub mod audience;
pub mod mark;
pub mod next;
pub mod play;
