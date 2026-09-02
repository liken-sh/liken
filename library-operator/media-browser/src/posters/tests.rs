use std::collections::HashMap;
use std::fs;
use std::ops::Range;
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use image::{Rgb, RgbImage, Rgba, RgbaImage};

use super::store::{ArtStore, Poster};
use super::{Art, Fit, Posters};
use crate::harness::Waker;

const GREEN: [u8; 4] = [0, 255, 0, 255];
const RED: [u8; 4] = [255, 0, 0, 255];

struct Wakes {
    count: Mutex<usize>,
    signal: Condvar,
}

fn wakes() -> (Arc<Wakes>, Waker) {
    let wakes = Arc::new(Wakes {
        count: Mutex::new(0),
        signal: Condvar::new(),
    });
    let counted = wakes.clone();
    let waker: Waker = Arc::new(move || {
        *counted.count.lock().unwrap() += 1;
        counted.signal.notify_all();
    });
    (wakes, waker)
}

fn wait_for_wakes(wakes: &Wakes, at_least: usize) {
    let deadline = Instant::now() + Duration::from_secs(10);
    let mut count = wakes.count.lock().unwrap();
    while *count < at_least {
        let left = deadline.saturating_duration_since(Instant::now());
        assert!(
            !left.is_zero(),
            "timed out at {} of {at_least} wakes",
            *count
        );
        (count, _) = wakes.signal.wait_timeout(count, left).unwrap();
    }
}

fn settled_wakes(wakes: &Wakes) -> usize {
    std::thread::sleep(Duration::from_millis(150));
    *wakes.count.lock().unwrap()
}

struct Volume {
    root: PathBuf,
}

impl Volume {
    fn new(test: &str) -> Self {
        let root = std::env::temp_dir().join(format!("art-store-{test}-{}", std::process::id()));
        fs::create_dir_all(&root).unwrap();
        Self { root }
    }

    fn store(&self, budget: usize, waker: Waker) -> ArtStore {
        self.pool(budget, waker, 1)
    }

    fn pool(&self, budget: usize, waker: Waker, workers: usize) -> ArtStore {
        let roots = HashMap::from([("movies".to_owned(), self.root.clone())]);
        ArtStore::with_workers(roots, budget, waker, workers)
    }

    fn write_png(
        &self,
        name: &str,
        width: u32,
        height: u32,
        green_x: Range<u32>,
        green_y: Range<u32>,
    ) {
        let art = RgbaImage::from_fn(width, height, |x, y| {
            if green_x.contains(&x) && green_y.contains(&y) {
                Rgba(GREEN)
            } else {
                Rgba(RED)
            }
        });
        art.save(self.root.join(name)).unwrap();
    }

    fn write_jpeg(&self, name: &str, width: u32, height: u32) {
        let art = RgbImage::from_pixel(width, height, Rgb([10, 20, 30]));
        art.save(self.root.join(name)).unwrap();
    }
}

impl Drop for Volume {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}

fn pixel_at(poster: &Poster, x: u32, y: u32) -> [u8; 4] {
    let at = ((y * poster.width + x) * 4) as usize;
    poster.rgba[at..at + 4].try_into().expect("four bytes")
}

fn assert_solid(poster: &Poster, pixel: [u8; 4]) {
    assert_eq!(
        poster.rgba.len(),
        (poster.width * poster.height * 4) as usize
    );
    let off = poster
        .rgba
        .chunks_exact(4)
        .position(|drawn| *drawn != pixel);
    assert_eq!(off, None, "a pixel differs from {pixel:?}");
}

#[test]
fn a_miss_decodes_in_the_background_and_becomes_a_hit() {
    let volume = Volume::new("miss-to-hit");
    volume.write_jpeg("poster.jpg", 64, 48);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "poster.jpg", 32, 32, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    let poster = store
        .poster("movies", "poster.jpg", 32, 32, Fit::Cover)
        .unwrap();
    assert_eq!(poster.width, 32);
    assert_eq!(poster.height, 32);
    assert_eq!(poster.rgba.len(), 32 * 32 * 4);
}

#[test]
fn a_wide_source_is_scaled_to_cover_and_cropped_to_its_center() {
    let volume = Volume::new("wide-crop");
    volume.write_png("wide.png", 60, 20, 20..40, 0..20);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "wide.png", 20, 20, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    let poster = store
        .poster("movies", "wide.png", 20, 20, Fit::Cover)
        .unwrap();
    assert_solid(&poster, GREEN);
}

#[test]
fn a_tall_source_is_scaled_to_cover_and_cropped_to_its_center() {
    let volume = Volume::new("tall-crop");
    volume.write_png("tall.png", 20, 60, 0..20, 20..40);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "tall.png", 20, 20, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    let poster = store
        .poster("movies", "tall.png", 20, 20, Fit::Cover)
        .unwrap();
    assert_solid(&poster, GREEN);
}

#[test]
fn the_least_recently_used_poster_leaves_when_the_budget_fills() {
    let volume = Volume::new("lru");
    volume.write_png("a.png", 8, 8, 0..8, 0..8);
    volume.write_png("b.png", 8, 8, 0..8, 0..8);
    volume.write_png("c.png", 8, 8, 0..8, 0..8);
    let (wakes, waker) = wakes();
    let mut store = volume.store(512, waker);
    assert!(store.poster("movies", "a.png", 8, 8, Fit::Cover).is_none());
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "b.png", 8, 8, Fit::Cover).is_none());
    wait_for_wakes(&wakes, 2);
    assert!(store.poster("movies", "b.png", 8, 8, Fit::Cover).is_some());
    assert!(store.poster("movies", "a.png", 8, 8, Fit::Cover).is_some());
    assert!(store.poster("movies", "c.png", 8, 8, Fit::Cover).is_none());
    wait_for_wakes(&wakes, 3);
    assert!(store.poster("movies", "a.png", 8, 8, Fit::Cover).is_some());
    assert!(store.poster("movies", "c.png", 8, 8, Fit::Cover).is_some());
    assert!(store.poster("movies", "b.png", 8, 8, Fit::Cover).is_none());
}

#[test]
fn a_missing_file_is_cached_as_a_failure_and_never_retried() {
    let volume = Volume::new("missing");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "absent.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    assert!(
        store
            .poster("movies", "absent.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    assert!(
        store
            .poster("movies", "absent.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn a_failed_decode_marks_a_delivery_the_way_a_poster_does() {
    let volume = Volume::new("delivered-failure");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(!store.delivered());
    assert!(
        store
            .poster("movies", "absent.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    assert!(store.delivered());
    assert!(!store.delivered());
}

#[test]
fn a_corrupt_file_is_cached_as_a_failure_and_never_retried() {
    let volume = Volume::new("corrupt");
    fs::write(volume.root.join("bad.jpg"), b"this is not an image").unwrap();
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "bad.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    assert!(
        store
            .poster("movies", "bad.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn repeated_requests_for_one_key_decode_once() {
    let volume = Volume::new("dedupe");
    volume.write_png("big.png", 512, 768, 0..512, 0..768);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 24, waker);
    assert!(
        store
            .poster("movies", "big.png", 300, 450, Fit::Cover)
            .is_none()
    );
    store.poster("movies", "big.png", 300, 450, Fit::Cover);
    store.poster("movies", "big.png", 300, 450, Fit::Cover);
    store.poster("movies", "big.png", 300, 450, Fit::Cover);
    wait_for_wakes(&wakes, 1);
    assert!(
        store
            .poster("movies", "big.png", 300, 450, Fit::Cover)
            .is_some()
    );
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn the_default_pool_decodes_too() {
    let volume = Volume::new("default-pool");
    volume.write_jpeg("poster.jpg", 24, 36);
    let (wakes, waker) = wakes();
    let roots = HashMap::from([("movies".to_owned(), volume.root.clone())]);
    let mut store = ArtStore::new(roots, 1 << 20, waker);
    assert!(
        store
            .poster("movies", "poster.jpg", 12, 18, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    assert!(
        store
            .poster("movies", "poster.jpg", 12, 18, Fit::Cover)
            .is_some()
    );
}

#[test]
fn empty_art_answers_none_without_a_decode() {
    let volume = Volume::new("empty-art");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("movies", "", 16, 16, Fit::Cover).is_none());
    assert_eq!(settled_wakes(&wakes), 0);
}

#[test]
fn an_unknown_library_answers_none_without_a_decode() {
    let volume = Volume::new("unknown-library");
    volume.write_jpeg("poster.jpg", 8, 8);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("shows", "poster.jpg", 16, 16, Fit::Cover)
            .is_none()
    );
    assert_eq!(settled_wakes(&wakes), 0);
}

#[test]
fn a_zero_sized_box_answers_none_without_a_decode() {
    let volume = Volume::new("zero-box");
    volume.write_jpeg("poster.jpg", 8, 8);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "poster.jpg", 0, 16, Fit::Cover)
            .is_none()
    );
    assert!(
        store
            .poster("movies", "poster.jpg", 16, 0, Fit::Cover)
            .is_none()
    );
    assert_eq!(settled_wakes(&wakes), 0);
}

#[test]
fn a_contain_fit_keeps_the_ends_a_cover_fit_crops() {
    let volume = Volume::new("contain");
    volume.write_png("wide.png", 60, 20, 0..10, 0..20);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);

    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    let covered = store
        .poster("movies", "wide.png", 30, 30, Fit::Cover)
        .unwrap();
    assert_eq!((covered.width, covered.height), (30, 30));
    assert_solid(&covered, RED);

    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Contain)
            .is_none()
    );
    wait_for_wakes(&wakes, 2);
    let fitted = store
        .poster("movies", "wide.png", 30, 30, Fit::Contain)
        .unwrap();
    assert_eq!((fitted.width, fitted.height), (30, 10));
    assert_eq!(pixel_at(&fitted, 0, 0), GREEN);
    assert_eq!(pixel_at(&fitted, 29, 0), RED);
}

#[test]
fn the_two_fits_of_one_file_are_two_cache_entries() {
    let volume = Volume::new("fit-key");
    volume.write_png("wide.png", 60, 20, 0..10, 0..20);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 1);
    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Contain)
            .is_none()
    );
    wait_for_wakes(&wakes, 2);
    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Cover)
            .is_some()
    );
    assert!(
        store
            .poster("movies", "wide.png", 30, 30, Fit::Contain)
            .is_some()
    );
    assert_eq!(settled_wakes(&wakes), 2);
}

// The page lane runs one decode at a time, and a pool asked for two
// page-size decodes and a slot-size one still delivers all three.
#[test]
fn a_pool_delivers_page_size_and_slot_size_decodes_together() {
    let volume = Volume::new("page-lane");
    volume.write_jpeg("first.jpg", 1400, 900);
    volume.write_jpeg("second.jpg", 1400, 900);
    volume.write_jpeg("slot.jpg", 64, 48);
    let (wakes, waker) = wakes();
    let mut store = volume.pool(1 << 26, waker, 2);
    assert!(
        store
            .poster("movies", "first.jpg", 1280, 800, Fit::Cover)
            .is_none()
    );
    assert!(
        store
            .poster("movies", "second.jpg", 1280, 800, Fit::Cover)
            .is_none()
    );
    assert!(
        store
            .poster("movies", "slot.jpg", 32, 32, Fit::Cover)
            .is_none()
    );
    wait_for_wakes(&wakes, 3);
    assert!(
        store
            .poster("movies", "first.jpg", 1280, 800, Fit::Cover)
            .is_some()
    );
    assert!(
        store
            .poster("movies", "second.jpg", 1280, 800, Fit::Cover)
            .is_some()
    );
    assert!(
        store
            .poster("movies", "slot.jpg", 32, 32, Fit::Cover)
            .is_some()
    );
}

// A store that holds no art at all, the shape the sample catalog and the
// browser's tests draw from.
struct NoArt;

impl Posters for NoArt {
    fn poster(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Art> {
        None
    }
}

#[test]
fn a_store_that_states_no_fit_of_its_own_answers_none() {
    assert!(NoArt.fitted("movies", "logo.png", 10, 10).is_none());
}
