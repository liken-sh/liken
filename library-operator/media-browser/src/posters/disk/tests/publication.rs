use std::fs;
use std::sync::{Arc, mpsc};
use std::time::Duration;

use tempfile::TempDir;

use super::super::*;
use super::{entry, key, resolve, source_poster, write_rgb};
use crate::posters::Fit;

const WAIT: Duration = Duration::from_secs(10);

fn cached_read_during_flush() -> Result {
    let dir = TempDir::new().unwrap();
    let cache_dir = dir.path().join("cache");
    let cached_source = dir.path().join("cached.jpg");
    let cached_key = key("cached.jpg", 24, 36, Fit::Cover);
    write_rgb(&cached_source, 48, 72, [30, 80, 140]);
    let cache = Arc::new(DiskCache::new(cache_dir));
    assert!(matches!(
        resolve(&cache, &cached_key, &cached_source),
        Result::Source(_)
    ));

    let publishing_key = key("publishing.png", 1, 1, Fit::Cover);
    let bytes = format::encode(
        &publishing_key,
        SourceStamp {
            size: 4,
            modified_ns: 5,
        },
        &source_poster([10, 20, 30, 100]),
    )
    .unwrap();
    let (flush_started_tx, flush_started_rx) = mpsc::sync_channel(0);
    let (release_flush_tx, release_flush_rx) = mpsc::sync_channel(0);
    let publishing_cache = cache.clone();
    let publishing = std::thread::spawn(move || {
        publishing_cache.state.as_ref().unwrap().publish_with_flush(
            &publishing_key,
            &bytes,
            |file| {
                flush_started_tx.send(()).unwrap();
                release_flush_rx.recv_timeout(WAIT).unwrap();
                file.sync_all()
            },
        )
    });
    flush_started_rx.recv_timeout(WAIT).unwrap();

    let (read_tx, read_rx) = mpsc::channel();
    let reading_cache = cache.clone();
    let reading = std::thread::spawn(move || {
        read_tx
            .send(resolve(&reading_cache, &cached_key, &cached_source))
            .unwrap();
    });
    let cached = read_rx.recv_timeout(WAIT);
    release_flush_tx.send(()).unwrap();
    publishing.join().unwrap().unwrap();
    reading.join().unwrap();

    cached.unwrap()
}

#[test]
fn flushing_one_entry_does_not_block_a_cached_read() {
    assert!(matches!(cached_read_during_flush(), Result::Cache(_)));
}

#[test]
fn a_publication_error_disables_later_writes_but_keeps_cached_reads() {
    let dir = TempDir::new().unwrap();
    let cache_dir = dir.path().join("cache");
    let cached_source = dir.path().join("cached.jpg");
    let failed_source = dir.path().join("failed.jpg");
    let cached_key = key("cached.jpg", 24, 36, Fit::Cover);
    let failed_key = key("failed.jpg", 24, 36, Fit::Cover);
    write_rgb(&cached_source, 48, 72, [30, 80, 140]);
    write_rgb(&failed_source, 48, 72, [140, 80, 30]);
    let cache = DiskCache::new(cache_dir.clone());
    assert!(matches!(
        resolve(&cache, &cached_key, &cached_source),
        Result::Source(_)
    ));
    let blocked_shard = entry(&cache_dir, &failed_key)
        .parent()
        .unwrap()
        .to_path_buf();
    assert_ne!(
        blocked_shard,
        entry(&cache_dir, &cached_key).parent().unwrap()
    );
    fs::write(&blocked_shard, b"not a directory").unwrap();

    assert!(matches!(
        resolve(&cache, &failed_key, &failed_source),
        Result::Source(_)
    ));
    fs::remove_file(&blocked_shard).unwrap();
    fs::create_dir(&blocked_shard).unwrap();
    assert!(matches!(
        resolve(&cache, &failed_key, &failed_source),
        Result::Source(_)
    ));
    assert!(!entry(&cache_dir, &failed_key).exists());
    fs::remove_file(&cached_source).unwrap();
    fs::remove_file(&failed_source).unwrap();

    assert!(matches!(
        resolve(&cache, &failed_key, &failed_source),
        Result::Source(None)
    ));
    assert!(matches!(
        resolve(&cache, &cached_key, &cached_source),
        Result::Cache(_)
    ));
}
