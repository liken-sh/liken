//! Reloading the resources when a file under the directory changes, so
//! the development loop is "change the YAML, reload the page" with no
//! restart.  A reload marks the cluster changed, as a watch event does,
//! so the next render reads the new resources and `transect` pushes
//! every open page its new rendering.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use notify::event::{AccessKind, AccessMode, ModifyKind};
use notify::{Event, EventKind, RecursiveMode, Watcher};
use tokio::sync::watch;

use super::Sorts;
use super::load::{is_yaml, load};

/// How long the directory must stay quiet before it is reread.  An
/// editor's save is several events, a write and a rename or two, and a
/// checkout of many files is a burst of them.
const QUIET: Duration = Duration::from_millis(300);

/// The resources under `dir` as loaded now, and again after each change
/// to a file under it.  Each reload sends on `changed`.  When the
/// directory cannot be watched, the first load stays in use, and a
/// warning in the log gives the reason.
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
        Err(error) => tracing::warn!(%error, "watching the resources directory"),
    });
    let mut watcher = match watcher {
        Ok(watcher) => watcher,
        Err(error) => {
            tracing::warn!(%error, "resources will not reload; restart transect after a change");
            return receiver;
        }
    };
    if let Err(error) = watcher.watch(&dir, RecursiveMode::Recursive) {
        tracing::warn!(dir = %dir.display(), %error, "resources will not reload; restart transect after a change");
        return receiver;
    }
    tokio::spawn(async move {
        // The watcher stops when dropped, so the task holds it.
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
                Err(error) => tracing::error!(%error, "reloading resources"),
            }
        }
    });
    receiver
}

/// Waits until `QUIET` passes with no further change.
async fn quiet(touches: &mut watch::Receiver<()>) {
    while let Ok(Ok(())) = tokio::time::timeout(QUIET, touches.changed()).await {}
}

/// Whether `event` can change what the directory holds.  Opening and
/// reading a file is an event too, and the reload itself reads every
/// file, so reads are ignored or each reload would start the next.
fn relevant(event: &Event) -> bool {
    let changes = match event.kind {
        EventKind::Access(AccessKind::Close(AccessMode::Write)) => true,
        EventKind::Access(_) | EventKind::Modify(ModifyKind::Metadata(_)) => false,
        EventKind::Other => false,
        _ => true,
    };
    changes && event.paths.iter().any(|path| names_resources(path))
}

/// A YAML file, or what may be a directory of them: an editor's swap
/// and backup files have extensions of their own and are left out.
fn names_resources(path: &Path) -> bool {
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
