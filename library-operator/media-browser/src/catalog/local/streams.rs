// The read behind a movie page's files section: every stream of one video
// file, and the text subtitle files mpv loads beside it.

use rusqlite::Connection;

use super::{collect, subtitles};
use crate::catalog::stream::{Stream, Tracks};

/// Every stream of one file in container order, and the subtitle files
/// beside it. The primary key of `streams` leads with the library and the
/// path, so the read is one range of it.
pub fn tracks(connection: &Connection, library: &str, path: &str) -> rusqlite::Result<Tracks> {
    let streams = collect(
        connection,
        "SELECT ordinal, kind, codec, profile, width, height, depth, frame_rate, \
                color_transfer, dolby_vision, channels, layout, language, title, \
                is_default, forced, events \
         FROM streams WHERE library = ? AND path = ? ORDER BY ordinal",
        &[&library, &path],
        |row| {
            Ok(Stream {
                ordinal: row.get(0)?,
                kind: row.get(1)?,
                codec: row.get(2)?,
                profile: row.get(3)?,
                width: row.get(4)?,
                height: row.get(5)?,
                depth: row.get(6)?,
                frame_rate: row.get(7)?,
                color_transfer: row.get(8)?,
                dolby_vision: row.get::<_, i64>(9)? != 0,
                channels: row.get(10)?,
                layout: row.get(11)?,
                language: row.get(12)?,
                title: row.get(13)?,
                default: row.get::<_, i64>(14)? != 0,
                forced: row.get::<_, i64>(15)? != 0,
                events: row.get(16)?,
            })
        },
    )?;
    Ok(Tracks {
        streams,
        beside: subtitles::beside(connection, library, path)?,
    })
}
