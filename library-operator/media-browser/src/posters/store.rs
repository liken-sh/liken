// The store's contract with the views: a hit answers at once, a miss
// queues a background decode and answers None, and the wake handle
// tells the loop to ask again once the decode lands.

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex, OnceLock};
use std::thread::{self, JoinHandle};

use super::cache::{Cache, Decoded};
use super::decode::decode_art;
use super::queue::RequestQueue;
use super::{Art, Fit};
use crate::harness::Waker;

// The pixel buffer is shared. A hit on every frame clones a pointer,
// not the pixels.
#[derive(Clone)]
pub struct Poster {
    pub width: u32,
    pub height: u32,
    pub rgba: Arc<[u8]>,
    // The handles over these pixels, built on the first ask and held with
    // the cache entry. The renderer keys its uploads by handle id, so a
    // frame that draws the same handles draws uploads it already holds.
    pub art: Arc<OnceLock<Art>>,
}

impl Poster {
    pub(crate) fn new(width: u32, height: u32, rgba: Arc<[u8]>) -> Self {
        Self {
            width,
            height,
            rgba,
            art: Arc::new(OnceLock::new()),
        }
    }
}

#[derive(Clone, PartialEq, Eq, Hash)]
struct Key {
    library: String,
    art: String,
    width: u32,
    height: u32,
    fit: Fit,
}

// A decode of more than this many pixels takes the page lane. A poster
// slot is about 0.1 megapixels and a backdrop at 1920x1080 is 2.07, so
// one megapixel separates the two with room either way.
const PAGE_PIXELS: u64 = 1_000_000;

struct Shared {
    cache: Cache<Key>,
    queue: RequestQueue<Key>,
    // A worker sets this after every insert, a success and a failure
    // alike, and the mark stands until a reader takes it, so a decode
    // that landed between two frames is never missed.
    delivered: bool,
    stop: bool,
}

pub struct ArtStore {
    roots: Arc<HashMap<String, PathBuf>>,
    shared: Arc<(Mutex<Shared>, Condvar)>,
    workers: Vec<JoinHandle<()>>,
}

impl ArtStore {
    // The pool caps at four workers, so decode work leaves cores for
    // the compositor and the rest of the machine.
    pub fn new(roots: HashMap<String, PathBuf>, budget: usize, waker: Waker) -> Self {
        let workers = thread::available_parallelism()
            .map(std::num::NonZeroUsize::get)
            .unwrap_or(1)
            .min(4);
        Self::with_workers(roots, budget, waker, workers)
    }

    pub fn with_workers(
        roots: HashMap<String, PathBuf>,
        budget: usize,
        waker: Waker,
        workers: usize,
    ) -> Self {
        let roots = Arc::new(roots);
        let shared = Arc::new((
            Mutex::new(Shared {
                cache: Cache::new(budget),
                queue: RequestQueue::default(),
                delivered: false,
                stop: false,
            }),
            Condvar::new(),
        ));
        let workers = (0..workers.max(1))
            .map(|_| spawn_worker(roots.clone(), shared.clone(), waker.clone()))
            .collect();
        Self {
            roots,
            shared,
            workers,
        }
    }

    // An item with no art, and a library the store holds no root for,
    // can never produce a poster, so neither reaches the queue.
    pub fn poster(
        &mut self,
        library: &str,
        art: &str,
        width: u32,
        height: u32,
        fit: Fit,
    ) -> Option<Poster> {
        if art.is_empty() || width == 0 || height == 0 {
            return None;
        }
        if !self.roots.contains_key(library) {
            return None;
        }
        let page = u64::from(width) * u64::from(height) > PAGE_PIXELS;
        let key = Key {
            library: library.to_owned(),
            art: art.to_owned(),
            width,
            height,
            fit,
        };
        let (lock, signal) = &*self.shared;
        let mut shared = lock.lock().expect("the store mutex is never poisoned");
        match shared.cache.get(&key) {
            Some(Decoded::Ready(poster)) => return Some(poster.clone()),
            Some(Decoded::Failed) => return None,
            None => {}
        }
        if shared.queue.request(key, page) {
            signal.notify_one();
        }
        None
    }

    // Take the mark a worker left, so the caller reads that a decode
    // landed and the mark is clear for the decodes after it.
    pub fn delivered(&mut self) -> bool {
        let (lock, _) = &*self.shared;
        let mut shared = lock.lock().expect("the store mutex is never poisoned");
        std::mem::take(&mut shared.delivered)
    }
}

// Dropping the store stops and joins the workers, so no decode outlives
// the screen that asked for it.
impl Drop for ArtStore {
    fn drop(&mut self) {
        let (lock, signal) = &*self.shared;
        lock.lock().expect("the store mutex is never poisoned").stop = true;
        signal.notify_all();
        for worker in self.workers.drain(..) {
            let _ = worker.join();
        }
    }
}

// The worker's loop: sleep until a request lands, decode with the lock
// released, insert the result, and wake the event loop.
fn spawn_worker(
    roots: Arc<HashMap<String, PathBuf>>,
    shared: Arc<(Mutex<Shared>, Condvar)>,
    waker: Waker,
) -> JoinHandle<()> {
    thread::spawn(move || {
        loop {
            let key = {
                let (lock, signal) = &*shared;
                let mut state = lock.lock().expect("the store mutex is never poisoned");
                loop {
                    if state.stop {
                        return;
                    }
                    if let Some(key) = state.queue.take() {
                        break key;
                    }
                    state = signal
                        .wait(state)
                        .expect("the store mutex is never poisoned");
                }
            };
            let path = roots[&key.library].join(&key.art);
            let value = match decode_art(&path, key.width, key.height, key.fit) {
                Some(poster) => Decoded::Ready(poster),
                None => Decoded::Failed,
            };
            {
                let (lock, _) = &*shared;
                let mut state = lock.lock().expect("the store mutex is never poisoned");
                state.cache.insert(key.clone(), value);
                state.queue.finish(&key);
                // The mark is set under the same lock as the insert
                // and before the wake fires, so the loop the wake
                // starts reads a mark the insert already left.
                state.delivered = true;
            }
            (*waker)();
        }
    })
}
