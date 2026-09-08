// The home page's reader. The browser asks it for a page and takes the
// page that landed. A source that gives a second read of its own reads
// on a thread and wakes the loop when the page is in hand, and a source
// that gives none reads in place on the ask, so a test reads the same
// way with no thread.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Instant;

use crate::catalog::Source;
use crate::catalog::draw::Date;
use crate::harness::Waker;
use crate::screens::home::{self, Page};

/// What the browser asks for a home page. One read runs at a time, and
/// an ask made while one runs is served by one read after it, so a burst
/// of asks costs two reads and not one each.
pub struct Reader {
    // The thread over the second source, or nothing where the source
    // gave none and every read runs in place.
    thread: Option<Thread>,
    // The page that landed and no screen has taken yet.
    landed: Option<Page>,
    // Whether a read is in flight on the thread.
    reading: bool,
    // The date and the audience of the read that is due after the one in
    // flight.
    due: Option<Ask>,
    // Whether each read prints the milliseconds it took.
    timed: Arc<AtomicBool>,
}

impl Reader {
    /// The reader over this second source, or over none.
    pub fn new(source: Option<Box<dyn Source + Send>>) -> Self {
        let timed = Arc::new(AtomicBool::new(false));
        Self {
            thread: source.map(|source| Thread::spawn(source, timed.clone())),
            landed: None,
            reading: false,
            due: None,
            timed,
        }
    }

    /// Print the milliseconds each read takes, which the measured run
    /// asks for.
    pub fn timed(&self, timed: bool) {
        self.timed.store(timed, Ordering::Relaxed);
    }

    /// Ask for one page of this date, for these people. A read already in
    /// flight is left to land, and one read follows it.
    pub fn ask(
        &mut self,
        source: &mut dyn Source,
        today: Date,
        people: Vec<String>,
        letters: Vec<String>,
    ) {
        let ask = Ask {
            date: today,
            people,
            letters,
        };
        let Some(thread) = &self.thread else {
            self.landed = Some(read(source, &ask, &self.timed));
            return;
        };
        if self.reading {
            self.due = Some(ask);
            return;
        }
        self.reading = true;
        let _ = thread.asks.send(ask);
    }

    /// Whether a read is in flight, so a caller that asks on every pass of
    /// the loop does not queue a second read behind the one that runs.
    pub fn reading(&self) -> bool {
        self.reading
    }

    /// The page that landed, or nothing while none has. A read that was
    /// due starts here, once the one before it landed.
    pub fn take(&mut self) -> Option<Page> {
        if let Some(thread) = &self.thread {
            while let Ok(page) = thread.pages.try_recv() {
                self.landed = Some(page);
                self.reading = false;
            }
            if !self.reading
                && let Some(ask) = self.due.take()
            {
                self.reading = true;
                let _ = thread.asks.send(ask);
            }
        }
        self.landed.take()
    }

    /// Take the handle that wakes the loop, so a page that lands reaches
    /// the browser on the next pass and not on the next frame.
    pub fn wake_by(&mut self, wake: Waker) {
        if let Some(thread) = &self.thread {
            *thread.wake.lock().expect("no thread panics with the lock") = Some(wake);
        }
    }
}

// What one read is asked for: the date the day's draw is seeded by, and the
// people the continue-watching row is read for.
struct Ask {
    date: Date,
    people: Vec<String>,
    // The letters of those people, for the row's heading.
    letters: Vec<String>,
}

// The thread over the second source: the asks it reads on, the pages
// it answers, and the handle it wakes the loop with.
struct Thread {
    asks: Sender<Ask>,
    pages: Receiver<Page>,
    wake: Arc<Mutex<Option<Waker>>>,
}

impl Thread {
    // The thread runs until the browser drops, because the ask channel
    // closes with it and the read loop ends there. A read in flight at
    // that moment finishes and is dropped with the channel.
    fn spawn(mut source: Box<dyn Source + Send>, timed: Arc<AtomicBool>) -> Self {
        let (asks, dates) = mpsc::channel::<Ask>();
        let (answers, pages) = mpsc::channel::<Page>();
        let wake = Arc::new(Mutex::new(None));
        let woken: Arc<Mutex<Option<Waker>>> = wake.clone();
        thread::spawn(move || {
            while let Ok(ask) = dates.recv() {
                let page = read(&mut *source, &ask, &timed);
                let _ = answers.send(page);
                // The page is sent before the wake fires, so the pass the
                // wake starts takes a page that is already in the channel.
                let wake = woken
                    .lock()
                    .expect("no thread panics with the lock")
                    .clone();
                if let Some(wake) = wake {
                    wake();
                }
            }
        });
        Self { asks, pages, wake }
    }
}

// One read, and the milliseconds it took where the run measures them.
fn read(source: &mut dyn Source, ask: &Ask, timed: &AtomicBool) -> Page {
    let started = Instant::now();
    let page = home::read(source, ask.date, &ask.people, &ask.letters);
    if timed.load(Ordering::Relaxed) {
        let ms = started.elapsed().as_secs_f64() * 1_000.0;
        eprintln!("media-browser: the home page read in {ms:.1} ms");
    }
    page
}
