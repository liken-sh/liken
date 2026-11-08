// Which audio track and which subtitle track a play of a file takes. The
// browser cannot ask mpv, because nothing plays while a person reads a
// film's page, so it applies the same rules mpv and media-operator apply
// when the play starts, to the tracks the probe recorded. The movie page
// marks the two tracks it finds.

pub mod audio;
pub mod preferences;
pub mod subtitle;

pub use preferences::Preferences;

#[cfg(test)]
mod tests;
