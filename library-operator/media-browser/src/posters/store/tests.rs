use std::collections::HashMap;
use std::sync::{Arc, mpsc};
use std::time::Duration;

use image::{Rgb, RgbImage};
use tempfile::TempDir;

use super::*;

const DEADLINE: Duration = Duration::from_secs(10);

fn store(source: &TempDir, cache: Option<PathBuf>) -> (ArtStore, mpsc::Receiver<()>) {
    let roots = HashMap::from([("movies".to_owned(), source.path().to_path_buf())]);
    let (sender, receiver) = mpsc::channel();
    let waker: Waker = Arc::new(move || {
        let _ = sender.send(());
    });
    (
        ArtStore::initialize(roots, 1 << 20, waker, 1, cache),
        receiver,
    )
}

fn request(store: &mut ArtStore, receiver: &mpsc::Receiver<()>, art: &str) {
    assert!(store.poster("movies", art, 16, 24, Fit::Cover).is_none());
    receiver.recv_timeout(DEADLINE).unwrap();
}

fn write_source(dir: &TempDir) {
    RgbImage::from_pixel(32, 48, Rgb([30, 80, 140]))
        .save(dir.path().join("poster.jpg"))
        .unwrap();
}

#[test]
fn a_source_decode_counts_once_and_a_memory_hit_does_not_count() {
    let source = TempDir::new().unwrap();
    write_source(&source);
    let (mut store, receiver) = store(&source, None);
    request(&mut store, &receiver, "poster.jpg");
    assert_eq!(
        store.counts(),
        PosterCounts {
            from_cache: 0,
            from_source: 1,
        }
    );
    assert!(
        store
            .poster("movies", "poster.jpg", 16, 24, Fit::Cover)
            .is_some()
    );
    assert_eq!(store.counts().from_source, 1);
}

#[test]
fn a_failed_source_read_counts_as_source_io() {
    let source = TempDir::new().unwrap();
    let (mut store, receiver) = store(&source, None);
    request(&mut store, &receiver, "missing.jpg");
    assert_eq!(
        store.counts(),
        PosterCounts {
            from_cache: 0,
            from_source: 1,
        }
    );
    assert!(
        store
            .poster("movies", "missing.jpg", 16, 24, Fit::Cover)
            .is_none()
    );
    assert_eq!(store.counts().from_source, 1);
}

#[test]
fn a_restart_uses_the_last_known_poster_only_when_the_source_is_missing() {
    let source = TempDir::new().unwrap();
    let cache = TempDir::new().unwrap();
    write_source(&source);
    let (mut first, receiver) = store(&source, Some(cache.path().to_path_buf()));
    request(&mut first, &receiver, "poster.jpg");
    assert_eq!(first.counts().from_source, 1);
    drop(first);
    std::fs::remove_file(source.path().join("poster.jpg")).unwrap();

    let (mut second, receiver) = store(&source, Some(cache.path().to_path_buf()));
    request(&mut second, &receiver, "poster.jpg");
    assert_eq!(
        second.counts(),
        PosterCounts {
            from_cache: 1,
            from_source: 0,
        }
    );
    assert!(
        second
            .poster("movies", "poster.jpg", 16, 24, Fit::Cover)
            .is_some()
    );
}
