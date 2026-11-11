// The titles fact's tool: where the end titles of a video start. `read`
// decodes the end of the video and writes, for each second, where text is
// on screen and what some of it says. `train` fits a small model on the
// readings of the videos that have a mark already. `place` gives the
// second the end titles start, and how sure the model is, from a reading
// and that model. Only `read` opens the video.

pub mod changepoint;
pub mod ctc;
pub mod engine;
pub mod frames;
pub mod model;
pub mod networks;
pub mod picture;
pub mod place;

pub mod read;
pub mod reading;
pub mod signals;

pub mod textboxes;
pub mod train;
pub mod trees;
pub mod words;

pub type Error = Box<dyn std::error::Error>;
