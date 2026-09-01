use std::collections::HashMap;
use std::fs;
use std::ops::Range;
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use image::{Rgb, RgbImage, Rgba, RgbaImage};

use super::store::{ArtStore, Poster};
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
        let roots = HashMap::from([("movies".to_owned(), self.root.clone())]);
        ArtStore::with_workers(roots, budget, waker, 1)
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
    assert!(store.poster("movies", "poster.jpg", 32, 32).is_none());
    wait_for_wakes(&wakes, 1);
    let poster = store.poster("movies", "poster.jpg", 32, 32).unwrap();
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
    assert!(store.poster("movies", "wide.png", 20, 20).is_none());
    wait_for_wakes(&wakes, 1);
    let poster = store.poster("movies", "wide.png", 20, 20).unwrap();
    assert_solid(&poster, GREEN);
}

#[test]
fn a_tall_source_is_scaled_to_cover_and_cropped_to_its_center() {
    let volume = Volume::new("tall-crop");
    volume.write_png("tall.png", 20, 60, 0..20, 20..40);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("movies", "tall.png", 20, 20).is_none());
    wait_for_wakes(&wakes, 1);
    let poster = store.poster("movies", "tall.png", 20, 20).unwrap();
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
    assert!(store.poster("movies", "a.png", 8, 8).is_none());
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "b.png", 8, 8).is_none());
    wait_for_wakes(&wakes, 2);
    assert!(store.poster("movies", "b.png", 8, 8).is_some());
    assert!(store.poster("movies", "a.png", 8, 8).is_some());
    assert!(store.poster("movies", "c.png", 8, 8).is_none());
    wait_for_wakes(&wakes, 3);
    assert!(store.poster("movies", "a.png", 8, 8).is_some());
    assert!(store.poster("movies", "c.png", 8, 8).is_some());
    assert!(store.poster("movies", "b.png", 8, 8).is_none());
}

#[test]
fn a_missing_file_is_cached_as_a_failure_and_never_retried() {
    let volume = Volume::new("missing");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("movies", "absent.jpg", 16, 16).is_none());
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "absent.jpg", 16, 16).is_none());
    assert!(store.poster("movies", "absent.jpg", 16, 16).is_none());
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn a_failed_decode_marks_a_delivery_the_way_a_poster_does() {
    let volume = Volume::new("delivered-failure");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(!store.delivered());
    assert!(store.poster("movies", "absent.jpg", 16, 16).is_none());
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
    assert!(store.poster("movies", "bad.jpg", 16, 16).is_none());
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "bad.jpg", 16, 16).is_none());
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn repeated_requests_for_one_key_decode_once() {
    let volume = Volume::new("dedupe");
    volume.write_png("big.png", 512, 768, 0..512, 0..768);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 24, waker);
    assert!(store.poster("movies", "big.png", 300, 450).is_none());
    store.poster("movies", "big.png", 300, 450);
    store.poster("movies", "big.png", 300, 450);
    store.poster("movies", "big.png", 300, 450);
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "big.png", 300, 450).is_some());
    assert_eq!(settled_wakes(&wakes), 1);
}

#[test]
fn the_default_pool_decodes_too() {
    let volume = Volume::new("default-pool");
    volume.write_jpeg("poster.jpg", 24, 36);
    let (wakes, waker) = wakes();
    let roots = HashMap::from([("movies".to_owned(), volume.root.clone())]);
    let mut store = ArtStore::new(roots, 1 << 20, waker);
    assert!(store.poster("movies", "poster.jpg", 12, 18).is_none());
    wait_for_wakes(&wakes, 1);
    assert!(store.poster("movies", "poster.jpg", 12, 18).is_some());
}

#[test]
fn empty_art_answers_none_without_a_decode() {
    let volume = Volume::new("empty-art");
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("movies", "", 16, 16).is_none());
    assert_eq!(settled_wakes(&wakes), 0);
}

#[test]
fn an_unknown_library_answers_none_without_a_decode() {
    let volume = Volume::new("unknown-library");
    volume.write_jpeg("poster.jpg", 8, 8);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("shows", "poster.jpg", 16, 16).is_none());
    assert_eq!(settled_wakes(&wakes), 0);
}

#[test]
fn a_zero_sized_box_answers_none_without_a_decode() {
    let volume = Volume::new("zero-box");
    volume.write_jpeg("poster.jpg", 8, 8);
    let (wakes, waker) = wakes();
    let mut store = volume.store(1 << 20, waker);
    assert!(store.poster("movies", "poster.jpg", 0, 16).is_none());
    assert!(store.poster("movies", "poster.jpg", 16, 0).is_none());
    assert_eq!(settled_wakes(&wakes), 0);
}
