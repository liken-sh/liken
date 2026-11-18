//! Reloads the resources when a file under the directory changes, so
//! you can edit the YAML without restarting `transect`.  Each reload
//! also sends on the `changed` channel, so the open pages render again
//! with the new resources.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use notify::event::{AccessKind, AccessMode, ModifyKind};
use notify::{Event, EventKind, RecursiveMode, Watcher};
use tokio::sync::watch;

use super::Sorts;
use super::load::{is_yaml, load};

/// How long the directory must see no events before it is read again.
/// An editor's save produces several events (a write and one or two
/// renames), and a checkout of many files produces a burst of them, so
/// reading on the first event would read half-written files.
const QUIET: Duration = Duration::from_millis(300);

/// Loads the resources under `dir`, and loads them again after each
/// change to a file under it.  When the directory cannot be watched,
/// logs a warning and the receiver keeps the first load.
pub fn watch<S: Sorts + Send + Sync + 'static>(
    dir: PathBuf,
    changed: watch::Sender<()>,
) -> watch::Receiver<Arc<S>> {
    let (loaded, receiver) = watch::channel(Arc::new(load::<S>(&dir)));
    let (touched, mut touches) = watch::channel(());
    let watcher = notify::recommended_watcher(move |event: notify::Result<Event>| match event {
        Ok(event) if relevant(&event) => {
            touched.send_replace(());
        }
        Ok(_) => {}
        Err(error) => {
            tracing::warn!(%error, "The watcher of the resources directory reported an error")
        }
    });
    let mut watcher = match watcher {
        Ok(watcher) => watcher,
        Err(error) => {
            tracing::warn!(%error, "The resources directory is not watched, so resources reload only when transect restarts");
            return receiver;
        }
    };
    if let Err(error) = watcher.watch(&dir, RecursiveMode::Recursive) {
        tracing::warn!(dir = %dir.display(), %error, "The resources directory is not watched, so resources reload only when transect restarts");
        return receiver;
    }
    tokio::spawn(async move {
        // The watcher stops when dropped, so the task owns it.
        let _watcher = watcher;
        while touches.changed().await.is_ok() {
            quiet(&mut touches).await;
            let reloaded = tokio::task::spawn_blocking({
                let dir = dir.clone();
                move || load::<S>(&dir)
            });
            match reloaded.await {
                Ok(reloaded) => {
                    loaded.send_replace(Arc::new(reloaded));
                    changed.send_replace(());
                }
                Err(error) => {
                    tracing::error!(%error, "The task that reloads the resources did not finish")
                }
            }
        }
    });
    receiver
}

/// Waits until `QUIET` passes with no event.
async fn quiet(touches: &mut watch::Receiver<()>) {
    while let Ok(Ok(())) = tokio::time::timeout(QUIET, touches.changed()).await {}
}

/// Whether `event` can change the resources.  Reads are ignored
/// because opening and reading a file also produces events, and a
/// reload reads every file, so each reload would trigger the next.
fn relevant(event: &Event) -> bool {
    let changes = match event.kind {
        EventKind::Access(AccessKind::Close(AccessMode::Write)) => true,
        EventKind::Access(_) | EventKind::Modify(ModifyKind::Metadata(_)) => false,
        EventKind::Other => false,
        _ => true,
    };
    changes && event.paths.iter().any(|path| may_hold_yaml(path))
}

/// A path with no extension can be a new or renamed directory of YAML
/// files.  An editor's swap and backup files have other extensions, so
/// they do not cause a reload.
fn may_hold_yaml(path: &Path) -> bool {
    is_yaml(path) || path.extension().is_none()
}

#[cfg(test)]
mod tests {
    use notify::event::{CreateKind, DataChange};
    use rstest::rstest;

    use super::*;

    fn event(kind: EventKind, path: &str) -> Event {
        Event::new(kind).add_path(path.into())
    }

    #[rstest]
    #[case::a_write(
        EventKind::Modify(ModifyKind::Data(DataChange::Any)),
        "a/layers.yaml",
        true
    )]
    #[case::a_new_file(EventKind::Create(CreateKind::File), "a/overlays/web.yml", true)]
    #[case::a_new_directory(EventKind::Create(CreateKind::Folder), "a/overlays", true)]
    #[case::a_closed_write(
        EventKind::Access(AccessKind::Close(AccessMode::Write)),
        "a/layers.yaml",
        true
    )]
    #[case::a_read(
        EventKind::Access(AccessKind::Open(AccessMode::Any)),
        "a/layers.yaml",
        false
    )]
    #[case::an_editors_swap_file(EventKind::Create(CreateKind::File), "a/.layers.yaml.swp", false)]
    #[case::a_note(
        EventKind::Modify(ModifyKind::Data(DataChange::Any)),
        "a/notes.md",
        false
    )]
    fn reloads_for_changes_to_yaml(
        #[case] kind: EventKind,
        #[case] path: &str,
        #[case] reloads: bool,
    ) {
        assert_eq!(relevant(&event(kind, path)), reloads);
    }
}
