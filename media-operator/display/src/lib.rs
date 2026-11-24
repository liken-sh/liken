// The display is a library that `main.rs` calls, so the theme, canvas,
// fade, and IPC framing can be unit tested without opening a window.

pub mod arranger;
pub mod art;
pub mod canvas;
pub mod cast;
pub mod clock;
pub mod fade;
pub mod film;
pub mod focus;
pub mod header;
pub mod images;
pub mod ipc;
pub mod listing;
pub mod marks;
pub mod motion;
pub mod presentation;
pub mod record;
pub mod scrubber;
pub mod skip;
pub mod streams;
pub mod strip;
pub mod subtitles;
pub mod theme;
pub mod timing;
pub mod track;
pub mod upnext;
pub mod volume;
pub mod window;
