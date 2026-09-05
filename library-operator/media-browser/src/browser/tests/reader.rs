// The home page's reader at the browser: a page read on a thread of its
// own, what lands on the screen, and how many reads two asks take.

use std::thread;
use std::time::{Duration, Instant};

use super::*;

// A browser whose catalog answers a second source, so every home read
// after the first one runs on a thread. The clone the reader holds is
// the catalog as it stood here, and a change to the browser's own source
// after this never reaches it.
fn threaded() -> Browser<Fake, NoPosters> {
    let mut browser = Browser::new(
        Fake {
            movies: 3,
            recent: true,
            threaded: true,
            ..Fake::default()
        },
        NoPosters::default(),
    );
    browser.source.reads.store(0, Ordering::SeqCst);
    browser.source.calls.clear();
    browser
}

// Pump until a page lands, which a read on a thread takes milliseconds
// to answer.
fn settled(browser: &mut Browser<Fake, NoPosters>) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while Instant::now() < deadline {
        if browser.pump(2.0) {
            return;
        }
        thread::sleep(Duration::from_millis(5));
    }
    panic!("no page landed");
}

// Pump until the catalog has answered this many reads, and answer how
// many it read.
fn reads(browser: &mut Browser<Fake, NoPosters>, count: usize) -> usize {
    let deadline = Instant::now() + Duration::from_secs(5);
    while Instant::now() < deadline && browser.source.reads.load(Ordering::SeqCst) < count {
        browser.pump(2.0);
        thread::sleep(Duration::from_millis(5));
    }
    browser.source.reads.load(Ordering::SeqCst)
}

// A tenth of a second of pumps, in which a read the reader should not
// have asked for would land.
fn quiet(browser: &mut Browser<Fake, NoPosters>) {
    for _ in 0..20 {
        browser.pump(2.0);
        thread::sleep(Duration::from_millis(5));
    }
}

// The headings of the page's strips, in the page's order.
fn headings(browser: &Browser<Fake, NoPosters>) -> Vec<String> {
    showing_home(browser)
        .blocks
        .iter()
        .filter_map(|block| block.strip())
        .map(|strip| strip.heading.clone())
        .collect()
}

#[test]
fn a_page_read_on_the_thread_lands_on_a_later_pump() {
    let mut browser = threaded();
    browser.source.recent = false;
    browser.source.movies = 0;
    browser.source.changed = true;

    browser.pump(1.0);
    settled(&mut browser);

    assert_eq!(
        headings(&browser),
        [
            "Recently released",
            "Recently added",
            "Libraries",
            "Genres",
            "Franchises · 2"
        ]
    );
    assert_eq!(strip_at(&browser, 1).items.len(), 2);
    assert!(!browser.source.calls.contains(&"pool"));
}

#[test]
fn two_asks_take_two_reads_and_no_more() {
    let mut browser = threaded();
    browser.source.changed = true;
    browser.pump(1.0);
    browser.source.changed = true;
    browser.pump(1.1);

    assert_eq!(reads(&mut browser, 2), 2);

    quiet(&mut browser);
    assert_eq!(browser.source.reads.load(Ordering::SeqCst), 2);
}

#[test]
fn a_page_that_lands_wakes_the_loop() {
    let mut browser = threaded();
    let wakes = Arc::new(AtomicUsize::new(0));
    let counted = wakes.clone();
    Screen::wake_by(
        &mut browser,
        Arc::new(move || {
            counted.fetch_add(1, Ordering::SeqCst);
        }),
    );
    browser.source.changed = true;

    browser.pump(1.0);
    settled(&mut browser);
    quiet(&mut browser);

    assert!(wakes.load(Ordering::SeqCst) >= 1);
}

#[test]
fn a_measured_run_reads_the_home_page_the_same_way() {
    let mut browser = browser(3).with_timing(true);
    browser.source.changed = true;

    assert!(browser.pump(1.0));

    assert!(browser.source.calls.contains(&"pool"));
}
