// The adapter between the store and the views. It holds one volume
// root per library, refuses an art path that leaves its root, and
// wraps a decoded buffer in the handle the canvas draws.

use std::collections::HashMap;
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, Mutex};

use iced_widget::core::Bytes;
use iced_widget::image::Handle;

use super::Posters;
use super::store::ArtStore;
use crate::harness::Waker;
use crate::views::wall;

// The cache size in posters, from the head-to-head: its wall held 96
// decoded posters inside the 115 MB the whole client used.
pub const CACHED_POSTERS: usize = 96;

/// The cache bound in bytes for a wall this wide: the head-to-head's
/// poster count at the size this wall draws one slot, four bytes to
/// the pixel.
pub fn budget(width: u32) -> usize {
    let cells = wall::cells(width as f32);
    CACHED_POSTERS * cells.poster_width as usize * cells.poster_height as usize * 4
}

/// The poster store as the views see it: the library roots, the decode
/// cache under them, and the loop's wake handle.
pub struct Volumes {
    store: ArtStore,
    // The wake handle arrives after the store is built, because the
    // harness owns the loop it wakes. Every worker fires through this
    // cell, so a handle set late still reaches decodes queued early.
    wake: Arc<Mutex<Option<Waker>>>,
}

impl Volumes {
    /// A store over these library roots, keyed by the catalog's
    /// `library` column, holding decoded posters under `budget` bytes.
    pub fn new(roots: HashMap<String, PathBuf>, budget: usize) -> Self {
        let wake = Arc::new(Mutex::new(None::<Waker>));
        let held = wake.clone();
        let waker: Waker = Arc::new(move || {
            let wake = held
                .lock()
                .expect("the wake cell is never poisoned")
                .clone();
            if let Some(wake) = wake {
                wake();
            }
        });
        Self {
            store: ArtStore::new(roots, budget, waker),
            wake,
        }
    }
}

// The catalog's art path is data from a volume this client does not
// control, so a path that leaves its library root is refused, never
// joined onto the root and opened. `..` climbs out of the root, and an
// absolute path makes `Path::join` discard the root entirely.
fn contained(art: &str) -> bool {
    Path::new(art)
        .components()
        .all(|part| matches!(part, Component::Normal(_) | Component::CurDir))
}

impl Posters for Volumes {
    fn poster(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Handle> {
        if !contained(art) {
            return None;
        }
        let poster = self.store.poster(library, art, width, height)?;
        // The handle borrows the same pixels the cache holds, so the
        // frame copies none of them.
        Some(Handle::from_rgba(
            poster.width,
            poster.height,
            Bytes::from_owner(poster.rgba),
        ))
    }

    fn delivered(&mut self) -> bool {
        self.store.delivered()
    }

    fn wake_by(&mut self, wake: Waker) {
        *self.wake.lock().expect("the wake cell is never poisoned") = Some(wake);
    }
}

#[cfg(test)]
mod tests {
    use std::sync::mpsc;
    use std::time::Duration;

    use image::{Rgb, RgbImage};
    use tempfile::TempDir;

    use super::*;

    const DEADLINE: Duration = Duration::from_secs(10);

    fn volume(dir: &TempDir) -> Volumes {
        let art = RgbImage::from_pixel(120, 180, Rgb([40, 90, 160]));
        art.save(dir.path().join("poster.jpg")).unwrap();
        let roots = HashMap::from([("local/movies".to_owned(), dir.path().to_path_buf())]);
        Volumes::new(roots, 1 << 20)
    }

    #[test]
    fn a_decoded_poster_becomes_a_handle_of_the_drawn_size() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(
            volumes
                .poster("local/movies", "poster.jpg", 40, 60)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();

        let handle = volumes
            .poster("local/movies", "poster.jpg", 40, 60)
            .expect("the decode landed");
        let Handle::Rgba {
            width,
            height,
            pixels,
            ..
        } = handle
        else {
            panic!("a decoded poster is an Rgba handle");
        };
        assert_eq!((width, height), (40, 60));
        assert_eq!(pixels.len(), 40 * 60 * 4);
    }

    #[test]
    fn a_decode_marks_one_delivery_and_the_mark_clears() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(!volumes.delivered());
        assert!(
            volumes
                .poster("local/movies", "poster.jpg", 24, 36)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();

        assert!(volumes.delivered());
        assert!(!volumes.delivered());
    }

    #[test]
    fn a_path_that_leaves_its_root_is_refused() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        assert!(!contained("../poster.jpg"));
        assert!(!contained("art/../../poster.jpg"));
        assert!(!contained("/etc/hosts"));
        assert!(contained("art/./poster.jpg"));
        assert!(
            volumes
                .poster("local/movies", "../poster.jpg", 40, 60)
                .is_none()
        );
    }

    #[test]
    fn a_wake_before_the_handle_arrives_is_dropped() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        assert!(
            volumes
                .poster("local/movies", "poster.jpg", 8, 12)
                .is_none()
        );
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));
        assert!(volumes.poster("local/movies", "other.jpg", 8, 12).is_none());
        receiver.recv_timeout(DEADLINE).unwrap();
    }

    #[test]
    fn the_budget_holds_the_head_to_heads_posters() {
        let cells = wall::cells(1920.0);
        let one = cells.poster_width as usize * cells.poster_height as usize * 4;
        assert_eq!(budget(1920), CACHED_POSTERS * one);
        assert!(budget(1280) < budget(1920));
    }
}
