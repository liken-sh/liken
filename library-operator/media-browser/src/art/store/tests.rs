use std::collections::HashMap;
use std::sync::{Arc, mpsc};
use std::time::Duration;

use image::{Rgb, RgbImage};
use tempfile::TempDir;

use super::*;

const DEADLINE: Duration = Duration::from_secs(10);

fn store(source: &TempDir, cache: Option<PathBuf>) -> (Store, mpsc::Receiver<()>) {
    store_with_budget(source, cache, None)
}

fn store_with_budget(
    source: &TempDir,
    cache: Option<PathBuf>,
    budget: Option<usize>,
) -> (Store, mpsc::Receiver<()>) {
    let roots = HashMap::from([("movies".to_owned(), source.path().to_path_buf())]);
    let (sender, receiver) = mpsc::channel();
    let waker: Waker = Arc::new(move || {
        let _ = sender.send(());
    });
    (
        Store::initialize(roots, 1 << 20, waker, 1, cache, budget),
        receiver,
    )
}

fn request(store: &mut Store, receiver: &mpsc::Receiver<()>, art: &str) {
    assert!(store.scaled("movies", art, 16, 24, Fit::Cover).is_none());
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
        ArtCounts {
            from_cache: 0,
            from_source: 1,
        }
    );
    assert!(
        store
            .scaled("movies", "poster.jpg", 16, 24, Fit::Cover)
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
        ArtCounts {
            from_cache: 0,
            from_source: 1,
        }
    );
    assert!(
        store
            .scaled("movies", "missing.jpg", 16, 24, Fit::Cover)
            .is_none()
    );
    assert_eq!(store.counts().from_source, 1);
}

#[test]
fn a_budget_the_run_states_bounds_what_the_disk_cache_keeps() {
    let source = TempDir::new().unwrap();
    let cache = TempDir::new().unwrap();
    write_source(&source);
    let (mut first, receiver) =
        store_with_budget(&source, Some(cache.path().to_path_buf()), Some(1));
    request(&mut first, &receiver, "poster.jpg");
    drop(first);

    let (mut second, receiver) = store(&source, Some(cache.path().to_path_buf()));
    request(&mut second, &receiver, "poster.jpg");
    assert_eq!(
        second.counts(),
        ArtCounts {
            from_cache: 0,
            from_source: 1,
        }
    );
}

#[test]
fn a_restart_uses_the_last_known_art_only_when_the_source_is_missing() {
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
        ArtCounts {
            from_cache: 1,
            from_source: 0,
        }
    );
    assert!(
        second
            .scaled("movies", "poster.jpg", 16, 24, Fit::Cover)
            .is_some()
    );
}
