use std::fs::{self, File, FileTimes};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, UNIX_EPOCH};

use image::{Rgb, RgbImage, Rgba, RgbaImage};
use tempfile::TempDir;

use super::*;
use crate::posters::Fit;
use crate::posters::decode::decode_art;

mod publication;

fn key(art: &str, width: u32, height: u32, fit: Fit) -> Key {
    Key {
        library: "movies".to_owned(),
        art: art.to_owned(),
        width,
        height,
        fit,
    }
}

fn write_rgb(path: &Path, width: u32, height: u32, color: [u8; 3]) {
    RgbImage::from_pixel(width, height, Rgb(color))
        .save(path)
        .unwrap();
}

fn write_rgba(path: &Path, width: u32, height: u32, color: [u8; 4]) {
    RgbaImage::from_pixel(width, height, Rgba(color))
        .save(path)
        .unwrap();
}

fn resolve(cache: &DiskCache, key: &Key, source: &Path) -> Result {
    cache.resolve(key, source, || {
        decode_art(source, key.width, key.height, key.fit)
    })
}

fn entry(cache: &Path, key: &Key) -> PathBuf {
    trim::path(&cache.join("v1"), key).unwrap()
}

fn source_poster(color: [u8; 4]) -> Poster {
    Poster::new(1, 1, Arc::from(color))
}

#[test]
fn a_reopened_cache_answers_after_the_source_is_deleted() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache = dir.path().join("cache");
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    write_rgb(&source, 48, 72, [30, 80, 140]);
    assert!(matches!(
        resolve(&DiskCache::new(cache.clone()), &key, &source),
        Result::Source(_)
    ));
    fs::remove_file(&source).unwrap();
    assert!(matches!(
        resolve(&DiskCache::new(cache), &key, &source),
        Result::Cache(_)
    ));
}

#[test]
fn a_source_with_a_changed_size_replaces_the_cached_generation() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.png");
    let cache = DiskCache::new(dir.path().join("cache"));
    let key = key("poster.png", 16, 16, Fit::Cover);
    write_rgb(&source, 16, 16, [10, 20, 30]);
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    let original = source.metadata().unwrap();
    write_rgba(&source, 17, 16, [200, 20, 30, 100]);
    File::options()
        .write(true)
        .open(&source)
        .unwrap()
        .set_times(FileTimes::new().set_modified(original.modified().unwrap()))
        .unwrap();
    assert_ne!(source.metadata().unwrap().len(), original.len());
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    fs::remove_file(&source).unwrap();
    let Result::Cache(poster) = resolve(&cache, &key, &source) else {
        panic!("the replacement was cached");
    };
    assert_eq!(poster.rgba[3], 100);
}

#[test]
fn a_source_with_only_a_changed_mtime_is_a_miss() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.png");
    let cache = DiskCache::new(dir.path().join("cache"));
    let key = key("poster.png", 16, 16, Fit::Cover);
    write_rgb(&source, 16, 16, [10, 20, 30]);
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    let changed = source.metadata().unwrap().modified().unwrap() + Duration::from_secs(2);
    File::options()
        .write(true)
        .open(&source)
        .unwrap()
        .set_times(FileTimes::new().set_modified(changed))
        .unwrap();
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
}

#[test]
fn cover_contain_and_key_fields_have_separate_paths() {
    let version = Path::new("cache/v1");
    let cover = key("poster.png", 40, 60, Fit::Cover);
    let contain = key("poster.png", 40, 60, Fit::Contain);
    let other_art = key("other.png", 40, 60, Fit::Cover);
    assert_ne!(trim::path(version, &cover), trim::path(version, &contain));
    assert_ne!(trim::path(version, &cover), trim::path(version, &other_art));
}

#[test]
fn a_corrupt_entry_misses_and_is_repaired() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache_dir = dir.path().join("cache");
    let cache = DiskCache::new(cache_dir.clone());
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    write_rgb(&source, 48, 72, [30, 80, 140]);
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    fs::write(entry(&cache_dir, &key), b"truncated").unwrap();
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    fs::remove_file(&source).unwrap();
    assert!(matches!(
        resolve(&DiskCache::new(cache_dir), &key, &source),
        Result::Cache(_)
    ));
}

#[test]
fn startup_trim_removes_the_oldest_entry() {
    let dir = TempDir::new().unwrap();
    let cache_dir = dir.path().join("cache");
    let first_source = dir.path().join("first.jpg");
    let second_source = dir.path().join("second.jpg");
    let first = key("first.jpg", 24, 36, Fit::Cover);
    let second = key("second.jpg", 24, 36, Fit::Cover);
    write_rgb(&first_source, 48, 72, [30, 80, 140]);
    write_rgb(&second_source, 48, 72, [140, 80, 30]);
    let cache = DiskCache::new(cache_dir.clone());
    assert!(matches!(
        resolve(&cache, &first, &first_source),
        Result::Source(_)
    ));
    assert!(matches!(
        resolve(&cache, &second, &second_source),
        Result::Source(_)
    ));
    let first_entry = entry(&cache_dir, &first);
    let second_entry = entry(&cache_dir, &second);
    File::options()
        .write(true)
        .open(&first_entry)
        .unwrap()
        .set_times(FileTimes::new().set_modified(UNIX_EPOCH + Duration::from_secs(1)))
        .unwrap();
    File::options()
        .write(true)
        .open(&second_entry)
        .unwrap()
        .set_times(FileTimes::new().set_modified(UNIX_EPOCH + Duration::from_secs(2)))
        .unwrap();
    let budget = usize::try_from(second_entry.metadata().unwrap().len()).unwrap();
    drop(cache);
    let trimmed = DiskCache::with_budget(cache_dir, budget);
    fs::remove_file(&first_source).unwrap();
    fs::remove_file(&second_source).unwrap();
    assert!(matches!(
        resolve(&trimmed, &first, &first_source),
        Result::Source(None)
    ));
    assert!(matches!(
        resolve(&trimmed, &second, &second_source),
        Result::Cache(_)
    ));
}

#[test]
fn a_write_trims_the_oldest_entry_to_the_cap() {
    let dir = TempDir::new().unwrap();
    let cache_dir = dir.path().join("cache");
    let first_source = dir.path().join("first.jpg");
    let second_source = dir.path().join("second.jpg");
    let first = key("first.jpg", 24, 36, Fit::Cover);
    let second = key("second.jpg", 24, 36, Fit::Cover);
    write_rgb(&first_source, 48, 72, [30, 80, 140]);
    write_rgb(&second_source, 48, 72, [140, 80, 30]);
    let initial = DiskCache::new(cache_dir.clone());
    assert!(matches!(
        resolve(&initial, &first, &first_source),
        Result::Source(_)
    ));
    let first_entry = entry(&cache_dir, &first);
    let old = UNIX_EPOCH + Duration::from_secs(1);
    File::options()
        .write(true)
        .open(&first_entry)
        .unwrap()
        .set_times(FileTimes::new().set_modified(old))
        .unwrap();
    let first_size = usize::try_from(first_entry.metadata().unwrap().len()).unwrap();
    let second_poster =
        decode_art(&second_source, second.width, second.height, second.fit).unwrap();
    let second_stamp = source_stamp(&second_source).unwrap().unwrap();
    let second_size = format::encode(&second, second_stamp, &second_poster)
        .unwrap()
        .len();
    drop(initial);
    let cache = DiskCache::with_budget(cache_dir, first_size.max(second_size));
    assert!(matches!(
        resolve(&cache, &second, &second_source),
        Result::Source(_)
    ));
    fs::remove_file(&first_source).unwrap();
    fs::remove_file(&second_source).unwrap();
    assert!(matches!(
        resolve(&cache, &first, &first_source),
        Result::Source(None)
    ));
    assert!(matches!(
        resolve(&cache, &second, &second_source),
        Result::Cache(_)
    ));
}

#[test]
fn concurrent_writes_publish_one_complete_entry() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.png");
    let cache_dir = dir.path().join("cache");
    let cache = Arc::new(DiskCache::new(cache_dir.clone()));
    let key = key("poster.png", 64, 96, Fit::Cover);
    write_rgba(&source, 64, 96, [30, 80, 140, 100]);
    let first_cache = cache.clone();
    let first_key = key.clone();
    let first_source = source.clone();
    let first = std::thread::spawn(move || resolve(&first_cache, &first_key, &first_source));
    let second_cache = cache.clone();
    let second_key = key.clone();
    let second_source = source.clone();
    let second = std::thread::spawn(move || resolve(&second_cache, &second_key, &second_source));
    assert!(matches!(
        first.join().unwrap(),
        Result::Source(_) | Result::Cache(_)
    ));
    assert!(matches!(
        second.join().unwrap(),
        Result::Source(_) | Result::Cache(_)
    ));
    fs::remove_file(&source).unwrap();
    assert!(matches!(
        resolve(&DiskCache::new(cache_dir.clone()), &key, &source),
        Result::Cache(_)
    ));
    assert_eq!(
        fs::read_dir(entry(&cache_dir, &key).parent().unwrap())
            .unwrap()
            .count(),
        1
    );
}

#[test]
fn startup_removes_temp_files_and_ignores_other_files() {
    let dir = TempDir::new().unwrap();
    let cache_dir = dir.path().join("cache");
    let shard = cache_dir.join("v1/aa");
    fs::create_dir_all(&shard).unwrap();
    fs::write(shard.join(".tmp-abandoned"), b"partial").unwrap();
    fs::write(shard.join("notes"), b"unrelated").unwrap();
    fs::write(cache_dir.join("keep"), b"unrelated").unwrap();
    let cache = DiskCache::new(cache_dir.clone());
    assert!(cache.state.is_some());
    assert!(!shard.join(".tmp-abandoned").exists());
    assert!(shard.join("notes").exists());
    assert!(cache_dir.join("keep").exists());
}

#[test]
fn a_cache_path_that_is_a_file_falls_back_to_the_source() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache = dir.path().join("cache");
    write_rgb(&source, 48, 72, [30, 80, 140]);
    fs::write(&cache, b"not a directory").unwrap();
    assert!(matches!(
        resolve(
            &DiskCache::new(cache),
            &key("poster.jpg", 24, 36, Fit::Cover),
            &source
        ),
        Result::Source(_)
    ));
}

#[test]
fn a_truly_unwritable_cache_path_falls_back_to_the_source() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    write_rgb(&source, 48, 72, [30, 80, 140]);
    let cache = PathBuf::from(format!("/proc/{}/poster-cache", std::process::id()));
    assert!(matches!(
        resolve(
            &DiskCache::new(cache),
            &key("poster.jpg", 24, 36, Fit::Cover),
            &source
        ),
        Result::Source(_)
    ));
}

#[test]
fn a_failed_rename_removes_its_temporary_file() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache_dir = dir.path().join("cache");
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    write_rgb(&source, 48, 72, [30, 80, 140]);
    let cache = DiskCache::new(cache_dir.clone());
    let final_path = entry(&cache_dir, &key);
    fs::create_dir_all(&final_path).unwrap();
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    assert_eq!(
        fs::read_dir(final_path.parent().unwrap()).unwrap().count(),
        1
    );
}

#[test]
fn a_write_failure_still_returns_the_source_decode() {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache_dir = dir.path().join("cache");
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    write_rgb(&source, 48, 72, [30, 80, 140]);
    let cache = DiskCache::new(cache_dir.clone());
    let shard = entry(&cache_dir, &key).parent().unwrap().to_path_buf();
    fs::write(&shard, b"not a directory").unwrap();
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
}

#[cfg(unix)]
#[test]
fn a_cache_hit_reads_no_source_bytes() {
    use std::os::unix::fs::PermissionsExt;

    let dir = TempDir::new().unwrap();
    let source = dir.path().join("poster.jpg");
    let cache = DiskCache::new(dir.path().join("cache"));
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    write_rgb(&source, 48, 72, [30, 80, 140]);
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    fs::set_permissions(&source, fs::Permissions::from_mode(0o000)).unwrap();
    let answer = resolve(&cache, &key, &source);
    fs::set_permissions(&source, fs::Permissions::from_mode(0o600)).unwrap();
    assert!(matches!(answer, Result::Cache(_)));
}

#[cfg(unix)]
#[test]
fn a_metadata_permission_error_refuses_the_stale_entry() {
    use std::os::unix::fs::PermissionsExt;

    let dir = TempDir::new().unwrap();
    let source_dir = dir.path().join("source");
    let source = source_dir.join("poster.jpg");
    let cache = DiskCache::new(dir.path().join("cache"));
    let key = key("poster.jpg", 24, 36, Fit::Cover);
    fs::create_dir(&source_dir).unwrap();
    write_rgb(&source, 48, 72, [30, 80, 140]);
    assert!(matches!(resolve(&cache, &key, &source), Result::Source(_)));
    fs::set_permissions(&source_dir, fs::Permissions::from_mode(0o000)).unwrap();
    let answer = resolve(&cache, &key, &source);
    fs::set_permissions(&source_dir, fs::Permissions::from_mode(0o700)).unwrap();
    assert!(matches!(answer, Result::Source(None)));
}

#[test]
fn a_source_change_during_decode_retries_once_and_persists_the_stable_pair() {
    let before = SourceStamp {
        size: 10,
        modified_ns: 20,
    };
    let changed = SourceStamp {
        size: 11,
        modified_ns: 21,
    };
    let mut decodes = 0;
    let mut decode = || {
        decodes += 1;
        Some(source_poster([decodes, 0, 0, 255]))
    };
    let mut stamps = [changed, changed].into_iter();
    let (poster, stable) = decode_stable(before, &mut decode, || Ok(stamps.next()));
    assert_eq!(decodes, 2);
    assert_eq!(poster.unwrap().rgba[0], 2);
    assert_eq!(stable, Some(changed));
}

#[test]
fn a_source_that_changes_twice_is_returned_but_not_persisted() {
    let before = SourceStamp {
        size: 10,
        modified_ns: 20,
    };
    let changed = SourceStamp {
        size: 11,
        modified_ns: 21,
    };
    let changed_again = SourceStamp {
        size: 12,
        modified_ns: 22,
    };
    let mut decode = || Some(source_poster([1, 2, 3, 255]));
    let mut stamps = [changed, changed_again].into_iter();
    let (poster, stable) = decode_stable(before, &mut decode, || Ok(stamps.next()));
    assert!(poster.is_some());
    assert_eq!(stable, None);
}

#[cfg(unix)]
#[test]
fn startup_does_not_follow_a_shard_symlink() {
    use std::os::unix::fs::symlink;

    let dir = TempDir::new().unwrap();
    let outside = dir.path().join("outside");
    let version = dir.path().join("cache/v1");
    fs::create_dir_all(&outside).unwrap();
    fs::create_dir_all(&version).unwrap();
    let outside_file = outside.join("a".repeat(62));
    fs::write(&outside_file, b"outside").unwrap();
    fs::write(outside.join(".tmp-outside"), b"outside").unwrap();
    symlink(&outside, version.join("aa")).unwrap();
    let cache = DiskCache::new(dir.path().join("cache"));
    assert!(cache.state.is_some());
    assert!(outside_file.exists());
    assert!(outside.join(".tmp-outside").exists());
}

#[test]
fn metadata_before_the_epoch_has_a_signed_identity() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("source");
    fs::write(&path, b"source").unwrap();
    File::options()
        .write(true)
        .open(&path)
        .unwrap()
        .set_times(FileTimes::new().set_modified(UNIX_EPOCH - Duration::from_secs(1)))
        .unwrap();
    assert!(
        SourceStamp::from_metadata(&path.metadata().unwrap())
            .unwrap()
            .modified_ns
            < 0
    );
}

#[test]
fn the_default_budget_is_five_hundred_twelve_mibibytes() {
    assert_eq!(DEFAULT_BUDGET, 512 * 1024 * 1024);
}

#[test]
fn the_index_accounts_for_only_final_entries() {
    let dir = TempDir::new().unwrap();
    let version = dir.path().join("v1");
    fs::create_dir(&version).unwrap();
    let index = Index::scan(&version, 100).unwrap();
    assert_eq!(index.used(), 0);
}
