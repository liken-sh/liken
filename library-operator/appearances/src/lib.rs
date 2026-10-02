// The appearances fact's tool: which credited person is on screen at each
// second of a title. `detect` opens the video and writes every face it
// finds with its vector. `match` names those faces from the cast's
// headshots and from each other, and writes the spans the liken display
// draws. `review` is a development tool that draws the result over the film
// in mpv.

pub mod detect;
pub mod frames;
pub mod gallery;
pub mod knn_guard;
pub mod matcher;
pub mod matches;
pub mod models;
pub mod picture;
pub mod player;
pub mod record;
pub mod review;
pub mod runtime;
pub mod sheets;
pub mod spans;
pub mod summary;
pub mod yunet;
