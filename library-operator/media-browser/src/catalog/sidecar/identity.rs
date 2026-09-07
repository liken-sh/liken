// The identity read: every alias one library holds for one item. The
// scanner writes one alias per provider id it read off the sidecar, and a
// folder with no provider id falls back to a path alias, so every item in
// the catalog names itself at least once.

use rusqlite::Connection;

use super::collect;
use crate::catalog::Identity;

// The aliases of one item, in alias order, so a provider that two aliases
// name resolves to the same id on every read. The read uses the
// aliases_library_item index.
const ALIASES: &str = "SELECT alias FROM aliases WHERE library = ?1 AND item = ?2 ORDER BY alias";

/// The identity of one item, as a list of one, so the read goes through the
/// same seam every other sidecar read does. The numbers stay 0 here, because
/// the aliases table holds no episode number; the caller sets them.
pub fn of(connection: &Connection, library: &str, item: &str) -> rusqlite::Result<Vec<Identity>> {
    let aliases: Vec<String> = collect(connection, ALIASES, &[&library, &item], |row| row.get(0))?;

    let mut identity = Identity::default();
    for alias in &aliases {
        identity.add(alias);
    }
    Ok(vec![identity])
}
