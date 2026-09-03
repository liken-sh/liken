// The Source over plan 06's delivery. Every read is a SQLite read of
// the sidecar's local file, and a background stream marks changes, so
// the views re-read the file and never ask a service.

use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::Ordering;

use rusqlite::{Connection, OpenFlags, Row};

use crate::catalog::{
    Credits, Episode, FileFacts, LibraryEntry, MovieDetails, MovieSet, Person, PlayItem, Selection,
    SeriesDetails, Source, Title, Work,
};
use crate::harness::Waker;

mod details;
mod files;
mod item;
mod people;
mod play;
mod series;
mod updates;

// The file opens read-only because only scanners write, through their
// agents. A write from here would bypass the agent's CRDT bookkeeping,
// and the row would never reach a peer.
pub struct SidecarSource {
    database: PathBuf,
    connection: Option<Connection>,
    shared: Arc<updates::Shared>,
}

impl SidecarSource {
    // `database` is the sidecar's SQLite file, and `api` is the agent's
    // loopback HTTP base. One stream per item table follows updates
    // from construction on, so an event before the first read still
    // marks a re-read and nothing lands unseen.
    pub fn new(database: impl Into<PathBuf>, api: &str) -> Self {
        let shared = Arc::new(updates::Shared::default());
        for table in ["movies", "series", "episodes"] {
            updates::follow(shared.clone(), api.to_string(), table);
        }
        Self {
            database: database.into(),
            connection: None,
            shared,
        }
    }

    // The connection opens on demand and drops on any failure, because
    // plan 06 lets the sidecar be absent for a moment. A missing or
    // half-born file reads as empty, and the next call retries. The
    // failure is logged, because a wall that draws nothing looks the
    // same whether the catalog is empty or the file is unreachable, and
    // the log is where the difference shows.
    fn read<T>(&mut self, run: impl Fn(&Connection) -> rusqlite::Result<Vec<T>>) -> Vec<T> {
        if self.connection.is_none() {
            let flags = OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX;
            match Connection::open_with_flags(&self.database, flags) {
                Ok(connection) => self.connection = Some(connection),
                Err(error) => {
                    eprintln!(
                        "media-browser: cannot open the catalog {}: {error}",
                        self.database.display()
                    );
                }
            }
        }
        let Some(connection) = self.connection.as_ref() else {
            return Vec::new();
        };
        match run(connection) {
            Ok(rows) => rows,
            Err(_) => {
                self.connection = None;
                Vec::new()
            }
        }
    }
}

impl Drop for SidecarSource {
    fn drop(&mut self) {
        self.shared.stop.store(true, Ordering::Release);
    }
}

// The kind names the item table. This closed match is the whole of
// what may reach the SQL text, so no caller-supplied string is ever
// formatted into a query.
fn item_table(kind: &str) -> Option<&'static str> {
    match kind {
        "movies" => Some("movies"),
        "series" => Some("series"),
        _ => None,
    }
}

fn collect<T>(
    connection: &Connection,
    sql: &str,
    params: &[&dyn rusqlite::ToSql],
    map: impl Fn(&Row<'_>) -> rusqlite::Result<T>,
) -> rusqlite::Result<Vec<T>> {
    let mut statement = connection.prepare(sql)?;
    let rows = statement.query_map(params, |row| map(row))?;
    rows.collect()
}

impl Source for SidecarSource {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        // A library has one kind, so the union yields one row per
        // library, and the outer ORDER BY gives the first screen its
        // order.
        let sql = "SELECT library, kind, items FROM (\
                     SELECT library, 'movies' AS kind, COUNT(*) AS items FROM movies GROUP BY library \
                     UNION ALL \
                     SELECT library, 'series' AS kind, COUNT(*) AS items FROM series GROUP BY library\
                   ) ORDER BY library";
        self.read(|connection| {
            collect(connection, sql, &[], |row| {
                Ok(LibraryEntry {
                    library: row.get(0)?,
                    kind: row.get(1)?,
                    items: row.get::<_, i64>(2)?.max(0) as u64,
                })
            })
        })
    }

    fn titles(&mut self, library: &str, kind: &str) -> Vec<Title> {
        let Some(table) = item_table(kind) else {
            return Vec::new();
        };
        let sql = format!(
            "SELECT {} FROM {table} WHERE library = ? ORDER BY sort_key",
            item::COLUMNS
        );
        self.read(|connection| collect(connection, &sql, &[&library], item::title))
    }

    fn movie(&mut self, library: &str, id: &str) -> Option<MovieDetails> {
        self.read(|connection| details::movie(connection, library, id))
            .into_iter()
            .next()
    }

    fn set(&mut self, library: &str, id: &str) -> Option<MovieSet> {
        self.read(|connection| details::set(connection, library, id))
            .into_iter()
            .next()
    }

    fn series(&mut self, library: &str, id: &str) -> Option<SeriesDetails> {
        self.read(|connection| series::series(connection, library, id))
            .into_iter()
            .next()
    }

    fn episodes(&mut self, library: &str, id: &str) -> Vec<Episode> {
        self.read(|connection| series::episodes(connection, library, id))
    }

    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem> {
        match selection {
            Selection::Movie { id } => self.read(|connection| play::movie(connection, library, id)),
            Selection::Trailer { id } => {
                self.read(|connection| play::trailer(connection, library, id))
            }
            Selection::Episode {
                series,
                season,
                episode,
            } => self
                .read(|connection| play::episodes(connection, library, series, *season, *episode)),
        }
    }

    fn credits(&mut self, library: &str, id: &str) -> Credits {
        self.read(|connection| people::credits(connection, library, id))
            .into_iter()
            .next()
            .unwrap_or_default()
    }

    fn files(&mut self, library: &str, item: &str) -> Vec<FileFacts> {
        self.read(|connection| files::files(connection, library, item))
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Person> {
        self.read(|connection| people::person(connection, library, path))
            .into_iter()
            .next()
    }

    fn works(&mut self, library: &str, path: &str) -> Vec<Work> {
        self.read(|connection| people::works(connection, library, path))
    }

    fn changed(&mut self) -> bool {
        self.shared.changed.swap(false, Ordering::AcqRel)
    }

    fn wake_by(&mut self, wake: Waker) {
        *self.shared.wake.lock().unwrap() = Some(wake);
    }
}

#[cfg(test)]
mod tests;
