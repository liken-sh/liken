//! The browser's own half of the bus. The `media-screen` crate holds
//! the connection, the key names, the focus gate, and the shade, so
//! The play request the browser publishes with a body of its own, and the
//! offer block it writes onto that request and reads back when a person
//! takes the offer.

pub mod next;
pub mod play;
