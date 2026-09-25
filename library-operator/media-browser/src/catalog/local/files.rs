// The read behind the foot of a page: every file of one item, joined
// through the file items table, with the columns the foot draws.

use rusqlite::Connection;

use super::collect;
use crate::catalog::FileFacts;

/// Every file of one item, in path order, so a title with two encodings
/// draws its lines in the same order on every read.
pub fn files(
    connection: &Connection,
    library: &str,
    item: &str,
) -> rusqlite::Result<Vec<FileFacts>> {
    let sql = "SELECT files.role, files.type, files.container, files.video_codec, \
                      files.audio_codec, files.width, files.height, files.size_bytes, \
                      files.language \
               FROM files \
               JOIN file_items ON file_items.library = files.library \
               AND file_items.path = files.path \
               WHERE file_items.library = ? AND file_items.item = ? \
               ORDER BY files.path";
    collect(connection, sql, &[&library, &item], |row| {
        Ok(FileFacts {
            role: row.get(0)?,
            kind: row.get(1)?,
            container: row.get(2)?,
            video_codec: row.get(3)?,
            audio_codec: row.get(4)?,
            width: row.get(5)?,
            height: row.get(6)?,
            size_bytes: row.get(7)?,
            language: row.get(8)?,
        })
    })
}
