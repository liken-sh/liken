// The names a screen gives an audio or subtitle track: the role of a
// subtitle track and the format of each track. The language of a track is
// in `crate::language`. The media browser names the tracks of a file on a
// movie's page, and the film's display names them while the film plays, so
// both screens link this code and a track has one name on both.

pub mod format;
pub mod subtitle;

#[cfg(test)]
mod tests;
