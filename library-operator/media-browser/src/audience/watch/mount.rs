// A directory laid out the way the kubelet mounts a `ConfigMap`, and the
// kubelet's update of it, for the tests that watch the `Person` list.

use std::os::unix::fs::symlink;

use tempfile::TempDir;

use super::DATA;

/// A directory with `people.json` in it as the kubelet mounts it: the
/// contents in a timestamped directory, `..data` linked to that directory,
/// and `people.json` linked through `..data`.
pub(crate) fn mounted(contents: &str) -> TempDir {
    let dir = TempDir::new().unwrap();
    let version = "..2026_09_30_10_00_00.000000001";
    std::fs::create_dir(dir.path().join(version)).unwrap();
    std::fs::write(dir.path().join(version).join("people.json"), contents).unwrap();
    symlink(version, dir.path().join(DATA)).unwrap();
    symlink("..data/people.json", dir.path().join("people.json")).unwrap();
    dir
}

/// The kubelet's update: write the new contents into a new directory,
/// point a temporary link at it, and rename the link over `..data`.
pub(crate) fn swap(dir: &TempDir, version: &str, contents: &str) {
    let fresh = dir.path().join(version);
    std::fs::create_dir(&fresh).unwrap();
    std::fs::write(fresh.join("people.json"), contents).unwrap();
    let temporary = dir.path().join("..data_tmp");
    symlink(version, &temporary).unwrap();
    std::fs::rename(&temporary, dir.path().join(DATA)).unwrap();
}
