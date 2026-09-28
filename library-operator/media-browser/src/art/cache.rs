// The cache's bound is a byte budget over decoded buffers. When new
// art lands over the budget, the least recently drawn buffer leaves
// first.

use std::collections::HashMap;
use std::hash::Hash;

use super::store::Scaled;

pub(crate) enum Decoded {
    Ready(Scaled),
    Failed,
}

struct Slot {
    last_used: u64,
    value: Decoded,
}

pub(crate) struct Cache<K> {
    slots: HashMap<K, Slot>,
    used: usize,
    budget: usize,
    tick: u64,
}

impl<K: Clone + Eq + Hash> Cache<K> {
    pub(crate) fn new(budget: usize) -> Self {
        Self {
            slots: HashMap::new(),
            used: 0,
            budget,
            tick: 0,
        }
    }

    /// The bytes the cache holds right now, under its budget.
    pub(crate) fn used(&self) -> usize {
        self.used
    }

    pub(crate) fn get(&mut self, key: &K) -> Option<&Decoded> {
        self.tick += 1;
        let slot = self.slots.get_mut(key)?;
        slot.last_used = self.tick;
        Some(&slot.value)
    }

    // A failed decode is cached at zero bytes, so the wall does not
    // decode a bad file again. The byte eviction skips zero-byte
    // entries, because removing one frees no budget, so the failed
    // entries have a bound of their own: past FAILED, the least recently
    // asked one leaves, and a library of bad files cannot grow the map
    // without end.
    pub(crate) fn insert(&mut self, key: K, value: Decoded) {
        if let Some(old) = self.slots.remove(&key) {
            self.used -= bytes(&old.value);
        }
        if bytes(&value) == 0 {
            self.forget_a_failure();
        }
        let incoming = bytes(&value);
        self.make_room(incoming);
        self.tick += 1;
        self.used += incoming;
        self.slots.insert(
            key,
            Slot {
                last_used: self.tick,
                value,
            },
        );
    }
}

// How many failed decodes the cache remembers at most.
const FAILED: usize = 1024;

impl<K: Clone + Eq + Hash> Cache<K> {
    // Drops the least recently asked failed entry once the failed entries
    // reach their bound, so the next one has a place.
    /// Move the budget. A window that grew asks for larger decodes, and a
    /// budget sized for the smaller window would hold too few of them to
    /// draw one page, so every delivery evicts art the page still draws
    /// and the decodes never end. A budget that shrank evicts down to it
    /// at once.
    pub(crate) fn resize(&mut self, budget: usize) {
        self.budget = budget;
        self.make_room(0);
    }

    // Evict the least recently drawn art until this many more bytes fit.
    // An entry larger than the whole budget still lands, because the
    // loop stops when nothing is left to evict.
    fn make_room(&mut self, incoming: usize) {
        while self.used + incoming > self.budget {
            let Some(evict) = self
                .slots
                .iter()
                .filter(|(_, slot)| bytes(&slot.value) > 0)
                .min_by_key(|(_, slot)| slot.last_used)
                .map(|(key, _)| key.clone())
            else {
                break;
            };
            let gone = self.slots.remove(&evict).expect("the key was just found");
            self.used -= bytes(&gone.value);
        }
    }

    fn forget_a_failure(&mut self) {
        let failed = self
            .slots
            .values()
            .filter(|slot| bytes(&slot.value) == 0)
            .count();
        if failed < FAILED {
            return;
        }
        let Some(oldest) = self
            .slots
            .iter()
            .filter(|(_, slot)| bytes(&slot.value) == 0)
            .min_by_key(|(_, slot)| slot.last_used)
            .map(|(key, _)| key.clone())
        else {
            return;
        };
        self.slots.remove(&oldest);
    }
}

fn bytes(value: &Decoded) -> usize {
    match value {
        Decoded::Ready(scaled) => scaled.rgba.len(),
        Decoded::Failed => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::super::store::Scaled;
    use super::{Cache, Decoded, FAILED};

    fn scaled(bytes: usize) -> Decoded {
        Decoded::Ready(Scaled::new(1, 1, vec![0u8; bytes].into()))
    }

    fn is_ready(entry: Option<&Decoded>) -> bool {
        matches!(entry, Some(Decoded::Ready(_)))
    }

    #[test]
    fn used_reports_the_bytes_the_live_entries_hold() {
        let mut cache = Cache::new(512);
        assert_eq!(cache.used(), 0);
        cache.insert("a", scaled(256));
        cache.insert("bad", Decoded::Failed);
        assert_eq!(cache.used(), 256);
    }

    #[test]
    fn eviction_takes_the_least_recently_used() {
        let mut cache = Cache::new(512);
        cache.insert("a", scaled(256));
        cache.insert("b", scaled(256));
        assert!(is_ready(cache.get(&"b")));
        assert!(is_ready(cache.get(&"a")));
        cache.insert("c", scaled(256));
        assert!(is_ready(cache.get(&"a")));
        assert!(is_ready(cache.get(&"c")));
        assert!(cache.get(&"b").is_none());
    }

    #[test]
    fn failed_entries_consume_no_budget() {
        let mut cache = Cache::new(256);
        cache.insert("a", scaled(256));
        cache.insert("bad", Decoded::Failed);
        assert!(is_ready(cache.get(&"a")));
        assert!(matches!(cache.get(&"bad"), Some(Decoded::Failed)));
    }

    #[test]
    fn failed_entries_are_bounded_and_the_oldest_leaves_first() {
        let mut cache = Cache::new(256);
        for key in 0..FAILED {
            cache.insert(key, Decoded::Failed);
        }
        assert!(matches!(cache.get(&0), Some(Decoded::Failed)));
        cache.insert(FAILED, Decoded::Failed);
        assert!(matches!(cache.get(&0), Some(Decoded::Failed)));
        assert!(cache.get(&1).is_none());
        assert!(matches!(cache.get(&FAILED), Some(Decoded::Failed)));
        assert_eq!(cache.slots.len(), FAILED);
    }

    #[test]
    fn replacing_a_key_releases_its_old_bytes() {
        let mut cache = Cache::new(512);
        cache.insert("a", scaled(512));
        cache.insert("a", scaled(256));
        cache.insert("b", scaled(256));
        assert!(is_ready(cache.get(&"a")));
        assert!(is_ready(cache.get(&"b")));
    }

    #[test]
    fn a_smaller_budget_evicts_down_to_it_and_a_larger_one_keeps_everything() {
        let mut cache = Cache::new(300);
        cache.insert("a", scaled(100));
        cache.insert("b", scaled(100));
        cache.insert("c", scaled(100));
        cache.get(&"a");

        cache.resize(200);
        assert!(is_ready(cache.get(&"a")));
        assert!(!is_ready(cache.get(&"b")));
        assert!(is_ready(cache.get(&"c")));

        cache.resize(1000);
        cache.insert("d", scaled(100));
        assert!(is_ready(cache.get(&"a")));
        assert!(is_ready(cache.get(&"c")));
        assert!(is_ready(cache.get(&"d")));
    }

    #[test]
    fn an_entry_larger_than_the_budget_still_lands() {
        let mut cache = Cache::new(256);
        cache.insert("a", scaled(512));
        assert!(is_ready(cache.get(&"a")));
    }
}
