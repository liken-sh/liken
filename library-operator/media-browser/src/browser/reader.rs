// The home page's reader. The browser asks it for a page, or for the
// continue-watching row alone, and takes what landed. A source that gives
// a second read of its own reads on a thread and wakes the loop when the
// read is in hand, and a source that gives none reads in place on the ask,
// so a test reads the same way with no thread.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Instant;

use crate::catalog::Source;
use crate::catalog::draw::Date;
use crate::harness::Waker;
use crate::screens::home::{self, Page, Strip};

/// What the browser asks for a home page. One read runs at a time, and
/// an ask made while one runs is served by one read after it, so a burst
/// of asks costs two reads and not one each. A page answers every row, so
/// a row asked for while a page is due is served by the page.
pub struct Reader {
    // The thread over the second source, or nothing where the source
    // gave none and every read runs in place.
    thread: Option<Thread>,
    // The reads that landed and no screen has taken yet, in the order they
    // landed, so a row read after a page is applied after it.
    landed: Vec<Landed>,
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
            landed: Vec::new(),
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
        self.send(
            source,
            Ask {
                what: What::Page,
                date: today,
                people,
                letters,
            },
        );
    }

    /// Ask for the continue-watching row alone, for these people.
    pub fn ask_row(&mut self, source: &mut dyn Source, people: Vec<String>, letters: Vec<String>) {
        self.send(
            source,
            Ask {
                what: What::Row,
                date: Date::today(),
                people,
                letters,
            },
        );
    }

    fn send(&mut self, source: &mut dyn Source, ask: Ask) {
        let Some(thread) = &self.thread else {
            self.landed.push(read(source, &ask, &self.timed));
            return;
        };
        if self.reading {
            self.due = Some(match self.due.take() {
                Some(held) if held.what == What::Page && ask.what == What::Row => held,
                _ => ask,
            });
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

    /// The reads that landed, oldest first, or nothing while none has. A
    /// read that was due starts here, once the one before it landed.
    pub fn take(&mut self) -> Vec<Landed> {
        if let Some(thread) = &self.thread {
            while let Ok(landed) = thread.pages.try_recv() {
                self.landed.push(landed);
                self.reading = false;
            }
            if !self.reading
                && let Some(ask) = self.due.take()
            {
                self.reading = true;
                let _ = thread.asks.send(ask);
            }
        }
        std::mem::take(&mut self.landed)
    }

    /// Take the handle that wakes the loop, so a page that lands reaches
    /// the browser on the next pass and not on the next frame.
    pub fn wake_by(&mut self, wake: Waker) {
        if let Some(thread) = &self.thread {
            *thread.wake.lock().expect("no thread panics with the lock") = Some(wake);
        }
    }
}

/// What one read answered: a whole page, or the continue-watching row.
#[derive(Debug)]
pub enum Landed {
    Page(Page),
    Row(Strip),
}

// Which read an ask is for.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum What {
    Page,
    Row,
}

// What one read is asked for: the page or the row, the date the day's draw
// is seeded by, and the people the continue-watching row is read for.
struct Ask {
    what: What,
    date: Date,
    people: Vec<String>,
    // The letters of those people, for the row's heading.
    letters: Vec<String>,
}

// The thread over the second source: the asks it reads on, the pages
// it answers, and the handle it wakes the loop with.
struct Thread {
    asks: Sender<Ask>,
    pages: Receiver<Landed>,
    wake: Arc<Mutex<Option<Waker>>>,
}

impl Thread {
    // The thread runs until the browser drops, because the ask channel
    // closes with it and the read loop ends there. A read in flight at
    // that moment finishes and is dropped with the channel.
    fn spawn(mut source: Box<dyn Source + Send>, timed: Arc<AtomicBool>) -> Self {
        let (asks, dates) = mpsc::channel::<Ask>();
        let (answers, pages) = mpsc::channel::<Landed>();
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
fn read(source: &mut dyn Source, ask: &Ask, timed: &AtomicBool) -> Landed {
    let started = Instant::now();
    let (landed, what) = match ask.what {
        What::Page => (
            Landed::Page(home::read(source, ask.date, &ask.people, &ask.letters)),
            "home page",
        ),
        What::Row => (
            Landed::Row(home::read_row(source, &ask.people, &ask.letters)),
            "continue-watching row",
        ),
    };
    if timed.load(Ordering::Relaxed) {
        let ms = started.elapsed().as_secs_f64() * 1_000.0;
        eprintln!("media-browser: the {what} read in {ms:.1} ms");
    }
    landed
}
