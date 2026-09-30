// The watch on the `Person` list: the first read after the watch opens,
// the kubelet's swap of the `..data` link, a plain file written in place,
// and a file that does not parse. Each case waits on the waker with a
// timeout, so a watch that never fires fails the case instead of hanging
// it.

use std::sync::mpsc::{Receiver, channel};
use std::time::Duration;

use tempfile::TempDir;

use super::mount::{mounted, swap};
use super::*;

// The longest a case waits for one read.
const PATIENCE: Duration = Duration::from_secs(5);

const FIRST: &str = r#"[{"name":"person-a","displayName":"Person A"}]"#;
const SECOND: &str = r#"[{"name":"person-a","displayName":"Person A"},{"name":"person-b","displayName":"Person B"}]"#;

// The names in a list, in order.
fn names(people: &[Person]) -> Vec<&str> {
    people.iter().map(|person| person.name.as_str()).collect()
}

// The watch on this directory's `people.json`, the channel its waker sends
// on, and the first read, which the watch makes once it is open.
fn watching(dir: &TempDir) -> (Watch, Receiver<()>, Delivery) {
    let watch = Watch::start(dir.path().join("people.json"));
    let (sender, woken) = channel();
    watch.wake_by(Arc::new(move || {
        let _ = sender.send(());
    }));
    let first = next(&watch, &woken);
    (watch, woken, first)
}

// What the watch delivered by the next read. The first read may land
// before the waker is set, so the case looks for a delivery before it
// waits on the channel.
fn next(watch: &Watch, woken: &Receiver<()>) -> Delivery {
    let delivery = watch.take();
    if delivery != Delivery::default() {
        return delivery;
    }
    woken
        .recv_timeout(PATIENCE)
        .expect("the watch reads within the patience");
    watch.take()
}

#[test]
fn the_first_read_follows_the_open_watch() {
    let dir = mounted(FIRST);

    let (_watch, _woken, first) = watching(&dir);

    assert_eq!(names(&first.people.unwrap()), ["person-a"]);
    assert!(first.failures.is_empty());
}

#[test]
fn a_swap_of_the_data_link_reads_the_new_list() {
    let dir = mounted(FIRST);
    let (watch, woken, _) = watching(&dir);

    swap(&dir, "..2026_09_30_10_05_00.000000002", SECOND);

    let delivery = next(&watch, &woken);
    assert_eq!(names(&delivery.people.unwrap()), ["person-a", "person-b"]);
}

#[test]
fn a_swap_to_a_file_that_does_not_parse_delivers_a_failure_and_no_list() {
    let dir = mounted(FIRST);
    let (watch, woken, _) = watching(&dir);

    swap(&dir, "..2026_09_30_10_05_00.000000002", "not json");

    let delivery = next(&watch, &woken);
    assert_eq!(delivery.people, None);
    assert_eq!(delivery.failures.len(), 1);
    assert!(delivery.failures[0].contains("people.json"));
}

// A local run names a plain file, which an editor writes in place.
#[test]
fn a_plain_file_written_in_place_reads_the_new_list() {
    let dir = TempDir::new().unwrap();
    std::fs::write(dir.path().join("people.json"), FIRST).unwrap();
    let (watch, woken, _) = watching(&dir);

    std::fs::write(dir.path().join("people.json"), SECOND).unwrap();

    let delivery = next(&watch, &woken);
    assert_eq!(names(&delivery.people.unwrap()), ["person-a", "person-b"]);
}

// A write to another file in the directory is not a change of the list.
#[test]
fn a_write_to_another_file_reads_nothing() {
    let dir = mounted(FIRST);
    let (watch, woken, _) = watching(&dir);

    std::fs::write(dir.path().join("other.json"), "[]").unwrap();
    swap(&dir, "..2026_09_30_10_05_00.000000002", SECOND);

    let delivery = next(&watch, &woken);
    assert_eq!(names(&delivery.people.unwrap()), ["person-a", "person-b"]);
}

// A directory that is not there fails to open, and the watch waits to try
// again. The drop ends the wait at once, so the case returns.
#[test]
fn a_watch_on_a_missing_directory_stops_on_drop() {
    let dir = TempDir::new().unwrap();
    let watch = Watch::start(dir.path().join("missing").join("people.json"));

    drop(watch);
}

#[test]
fn a_bare_file_name_is_in_the_working_directory() {
    assert_eq!(
        split(Path::new("people.json")),
        (PathBuf::from("."), OsString::from("people.json"))
    );
}
