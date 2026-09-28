// The adapter between the store and the views. It holds one volume
// root per library, refuses an art path that leaves its root, and
// wraps a decoded buffer in the handle the canvas draws.

use std::collections::HashMap;
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, Mutex};

use iced_widget::core::Bytes;

use super::store::Store;
use super::{Art, ArtCounts, Fit, Image};
use crate::harness::Waker;
use crate::views::wall;

// The cache size in posters, from the head-to-head: its wall held 96
// decoded posters inside the 115 MB the whole client used.
pub const CACHED_POSTERS: usize = 96;

/// How many page-size backdrops the cache holds beside the posters. A
/// backdrop at 1920x1080 is 8.3 MB decoded, so three of them cost 24.9
/// MB. Plan 22's proof measures what that costs on the box.
pub const CACHED_BACKDROPS: usize = 3;

/// The cache bound in bytes for a window this size: the head-to-head's
/// poster count at the size this wall draws one slot, and the backdrops
/// a page draws at the size of the window, four bytes to the pixel.
pub fn budget(size: (u32, u32)) -> usize {
    let (width, height) = size;
    let cells = wall::cells(width as f32, wall::POSTER, wall::COLUMNS);
    CACHED_POSTERS * cells.poster_width as usize * cells.poster_height as usize * 4
        + CACHED_BACKDROPS * width as usize * height as usize * 4
}

/// The art store as the views see it: the library roots, the decode
/// cache under them, and the loop's wake handle.
pub struct Volumes {
    store: Store,
    // How many physical pixels one logical pixel spans. Every ask is in
    // logical pixels, and the decode under it is at physical size.
    scale: f32,
    // The wake handle arrives after the store is built, because the
    // harness owns the loop it wakes. Every worker fires through this
    // cell, so a handle set late still reaches decodes queued early.
    wake: Arc<Mutex<Option<Waker>>>,
}

impl Volumes {
    /// A store over these library roots, keyed by the catalog's
    /// `library` column, holding decoded art under `budget` bytes.
    pub fn new(roots: HashMap<String, PathBuf>, budget: usize) -> Self {
        Self::with_cache_dir(roots, budget, None, None)
    }

    /// A store with a disk cache where `cache_dir` names one, kept under
    /// `cache_budget` bytes, or under the cache's own default when the
    /// budget is `None`.
    pub fn with_cache_dir(
        roots: HashMap<String, PathBuf>,
        budget: usize,
        cache_dir: Option<PathBuf>,
        cache_budget: Option<usize>,
    ) -> Self {
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
            store: Store::with_cache_dir(roots, budget, waker, cache_dir, cache_budget),
            scale: 1.0,
            wake,
        }
    }

    // The handles borrow the same pixels the cache holds, so the frame
    // copies none of them. A cached decode builds its handles once and
    // holds them beside its pixels, so a redraw hands the renderer the
    // ids it already uploaded and uploads nothing again.
    fn decoded(
        &mut self,
        library: &str,
        art: &str,
        width: u32,
        height: u32,
        fit: Fit,
    ) -> Option<Image> {
        if !contained(art) {
            return None;
        }
        let width = (width as f32 * self.scale).round() as u32;
        let height = (height as f32 * self.scale).round() as u32;
        let scaled = self.store.scaled(library, art, width, height, fit)?;
        let built = scaled.art.get_or_init(|| {
            Image::at_scale(
                scaled.width,
                scaled.height,
                self.scale,
                Bytes::from_owner(scaled.rgba.clone()),
            )
        });
        Some(built.clone())
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

impl Art for Volumes {
    fn covered(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Image> {
        self.decoded(library, art, width, height, Fit::Cover)
    }

    fn fitted(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Image> {
        self.decoded(library, art, width, height, Fit::Contain)
    }

    fn scaled(&mut self, physical: (u32, u32), scale: f32) {
        self.scale = scale;
        self.store.resize(budget(physical));
    }

    fn file(&self, library: &str, path: &str) -> Option<PathBuf> {
        if !contained(path) {
            return None;
        }
        Some(self.store.root(library)?.join(path))
    }

    fn delivered(&mut self) -> bool {
        self.store.delivered()
    }

    fn counts(&self) -> ArtCounts {
        self.store.counts()
    }

    fn cache_bytes(&self) -> Option<usize> {
        Some(self.store.cache_bytes())
    }

    fn wake_by(&mut self, wake: Waker) {
        *self.wake.lock().expect("the wake cell is never poisoned") = Some(wake);
    }
}

#[cfg(test)]
mod tests {
    use std::sync::mpsc;
    use std::time::Duration;

    use iced_widget::core::Rectangle;
    use iced_widget::image::Handle;
    use image::{Rgb, RgbImage};
    use tempfile::TempDir;

    use super::*;

    const DEADLINE: Duration = Duration::from_secs(10);

    fn handles(art: &Image) -> Vec<Handle> {
        let (width, height) = art.size();
        art.bands(Rectangle {
            x: 0.0,
            y: 0.0,
            width: width as f32,
            height: height as f32,
        })
        .map(|(_, handle)| handle)
        .collect()
    }

    fn ids(art: &Image) -> Vec<iced_widget::core::image::Id> {
        handles(art).iter().map(Handle::id).collect()
    }

    fn volume(dir: &TempDir) -> Volumes {
        let art = RgbImage::from_pixel(120, 180, Rgb([40, 90, 160]));
        art.save(dir.path().join("poster.jpg")).unwrap();
        let roots = HashMap::from([("local/movies".to_owned(), dir.path().to_path_buf())]);
        Volumes::new(roots, 1 << 20)
    }

    #[test]
    fn decoded_art_becomes_a_handle_of_the_drawn_size() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(
            volumes
                .covered("local/movies", "poster.jpg", 40, 60)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();

        let art = volumes
            .covered("local/movies", "poster.jpg", 40, 60)
            .expect("the decode landed");
        assert_eq!(art.size(), (40, 60));
        let drawn = handles(&art);
        assert_eq!(drawn.len(), 1);
        let Handle::Rgba {
            width,
            height,
            pixels,
            ..
        } = &drawn[0]
        else {
            panic!("decoded art is an Rgba handle");
        };
        assert_eq!((*width, *height), (40, 60));
        assert_eq!(pixels.len(), 40 * 60 * 4);
        assert_eq!(
            volumes.counts(),
            ArtCounts {
                from_cache: 0,
                from_source: 1,
            }
        );
        assert_eq!(volumes.cache_bytes(), Some(40 * 60 * 4));
    }

    #[test]
    fn a_store_at_scale_two_decodes_twice_the_pixels_and_reports_the_logical_size() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        volumes.scaled((3840, 2160), 2.0);
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(
            volumes
                .covered("local/movies", "poster.jpg", 40, 60)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();

        let art = volumes
            .covered("local/movies", "poster.jpg", 40, 60)
            .expect("the decode landed");
        assert_eq!(art.size(), (40, 60));
        let Handle::Rgba { width, height, .. } = &handles(&art)[0] else {
            panic!("decoded art is an Rgba handle");
        };
        assert_eq!((*width, *height), (80, 120));
    }

    #[test]
    fn two_asks_for_one_decode_answer_the_same_handles() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(
            volumes
                .covered("local/movies", "poster.jpg", 40, 60)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();

        let first = volumes
            .covered("local/movies", "poster.jpg", 40, 60)
            .unwrap();
        let again = volumes
            .covered("local/movies", "poster.jpg", 40, 60)
            .unwrap();
        assert_eq!(ids(&first), ids(&again));
    }

    #[test]
    fn a_fitted_ask_keeps_the_whole_image_inside_its_box() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        let wide = RgbImage::from_pixel(120, 40, Rgb([200, 200, 200]));
        wide.save(dir.path().join("logo.png")).unwrap();
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));

        assert!(volumes.fitted("local/movies", "logo.png", 60, 60).is_none());
        receiver.recv_timeout(DEADLINE).unwrap();
        let fitted = volumes.fitted("local/movies", "logo.png", 60, 60).unwrap();
        assert_eq!(fitted.size(), (60, 20));

        assert!(
            volumes
                .covered("local/movies", "logo.png", 60, 60)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();
        let covered = volumes.covered("local/movies", "logo.png", 60, 60).unwrap();
        assert_eq!(covered.size(), (60, 60));
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
                .covered("local/movies", "poster.jpg", 24, 36)
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
                .covered("local/movies", "../poster.jpg", 40, 60)
                .is_none()
        );
        assert!(
            volumes
                .fitted("local/movies", "../poster.jpg", 40, 60)
                .is_none()
        );
    }

    #[test]
    fn a_wake_before_the_handle_arrives_is_dropped() {
        let dir = TempDir::new().unwrap();
        let mut volumes = volume(&dir);
        assert!(
            volumes
                .covered("local/movies", "poster.jpg", 8, 12)
                .is_none()
        );
        let (sender, receiver) = mpsc::channel();
        volumes.wake_by(Arc::new(move || {
            let _ = sender.send(());
        }));
        assert!(
            volumes
                .covered("local/movies", "other.jpg", 8, 12)
                .is_none()
        );
        receiver.recv_timeout(DEADLINE).unwrap();
    }

    #[test]
    fn a_file_beside_the_art_resolves_against_the_librarys_root() {
        let dir = TempDir::new().unwrap();
        let volumes = volume(&dir);
        assert_eq!(
            volumes.file("local/movies", ".contributors/One/biography.txt"),
            Some(dir.path().join(".contributors/One/biography.txt"))
        );
        assert_eq!(volumes.file("local/movies", "../escape.txt"), None);
        assert_eq!(volumes.file("local/none", "biography.txt"), None);
    }

    #[test]
    fn the_budget_holds_the_head_to_heads_posters_and_a_few_backdrops() {
        let cells = wall::cells(1920.0, wall::POSTER, wall::COLUMNS);
        let one = cells.poster_width as usize * cells.poster_height as usize * 4;
        let backdrops = CACHED_BACKDROPS * 1920 * 1080 * 4;
        assert_eq!(budget((1920, 1080)), CACHED_POSTERS * one + backdrops);
        assert!(budget((1280, 720)) < budget((1920, 1080)));
    }
}
