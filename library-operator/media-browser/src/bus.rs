//! The browser's own half of the bus. The `media-screen` crate holds
//! the connection, the key names, the focus gate, and the shade, so
//! what is left here is the one request the browser publishes with a
//! body of its own: the play list it resolved from the catalog.

pub mod play;
