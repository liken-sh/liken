// The cache's bound is a byte budget over decoded buffers. When a new
// poster lands over the budget, the least recently drawn one leaves
// first.

use std::collections::HashMap;
use std::hash::Hash;

use super::store::Poster;

pub(crate) enum Decoded {
    Ready(Poster),
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

    pub(crate) fn get(&mut self, key: &K) -> Option<&Decoded> {
        self.tick += 1;
        let slot = self.slots.get_mut(key)?;
        slot.last_used = self.tick;
        Some(&slot.value)
    }

    // A failed decode is cached at zero bytes, so the wall does not
    // decode a bad file again. Eviction skips zero-byte entries,
    // because removing one frees no budget.
    pub(crate) fn insert(&mut self, key: K, value: Decoded) {
        if let Some(old) = self.slots.remove(&key) {
            self.used -= bytes(&old.value);
        }
        let incoming = bytes(&value);
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

fn bytes(value: &Decoded) -> usize {
    match value {
        Decoded::Ready(poster) => poster.rgba.len(),
        Decoded::Failed => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::super::store::Poster;
    use super::{Cache, Decoded};

    fn poster(bytes: usize) -> Decoded {
        Decoded::Ready(Poster {
            width: 1,
            height: 1,
            rgba: vec![0u8; bytes].into(),
        })
    }

    fn is_ready(entry: Option<&Decoded>) -> bool {
        matches!(entry, Some(Decoded::Ready(_)))
    }

    #[test]
    fn eviction_takes_the_least_recently_used() {
        let mut cache = Cache::new(512);
        cache.insert("a", poster(256));
        cache.insert("b", poster(256));
        assert!(is_ready(cache.get(&"b")));
        assert!(is_ready(cache.get(&"a")));
        cache.insert("c", poster(256));
        assert!(is_ready(cache.get(&"a")));
        assert!(is_ready(cache.get(&"c")));
        assert!(cache.get(&"b").is_none());
    }

    #[test]
    fn failed_entries_consume_no_budget() {
        let mut cache = Cache::new(256);
        cache.insert("a", poster(256));
        cache.insert("bad", Decoded::Failed);
        assert!(is_ready(cache.get(&"a")));
        assert!(matches!(cache.get(&"bad"), Some(Decoded::Failed)));
    }

    #[test]
    fn replacing_a_key_releases_its_old_bytes() {
        let mut cache = Cache::new(512);
        cache.insert("a", poster(512));
        cache.insert("a", poster(256));
        cache.insert("b", poster(256));
        assert!(is_ready(cache.get(&"a")));
        assert!(is_ready(cache.get(&"b")));
    }

    #[test]
    fn an_entry_larger_than_the_budget_still_lands() {
        let mut cache = Cache::new(256);
        cache.insert("a", poster(512));
        assert!(is_ready(cache.get(&"a")));
    }
}
