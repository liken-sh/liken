//! Caches the values that a model derives, so later renders of the
//! same model reuse them. The next cluster change replaces the model,
//! and its caches are dropped with it.

use std::collections::HashMap;
use std::hash::Hash;
use std::sync::{Arc, Mutex};

/// A cache of the values for up to `cap` keys. A new key at the cap
/// clears the cache, so a viewer who tries many sets of edge kinds does
/// not grow it without limit.
pub struct Memo<K, V> {
    cap: usize,
    kept: Mutex<HashMap<K, Arc<V>>>,
}

impl<K: Eq + Hash, V> Memo<K, V> {
    pub fn new(cap: usize) -> Self {
        Self {
            cap,
            kept: Mutex::default(),
        }
    }

    /// Returns the cached value for `key`. When there is none, it calls
    /// `make`, caches the result, and returns it. `make` runs outside
    /// the lock, so a render that requests another key does not wait for
    /// it. Two renders that request the same new key at the same time
    /// can both call `make`, and the first result cached is kept.
    pub fn get(&self, key: K, make: impl FnOnce() -> V) -> Arc<V> {
        if let Some(kept) = self.lock().get(&key) {
            return kept.clone();
        }
        let made = Arc::new(make());
        let mut kept = self.lock();
        if kept.len() >= self.cap && !kept.contains_key(&key) {
            kept.clear();
        }
        kept.entry(key).or_insert(made).clone()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, HashMap<K, Arc<V>>> {
        self.kept
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

#[cfg(test)]
mod tests {
    use std::cell::Cell;

    use rstest::rstest;

    use super::*;

    /// Requests each key from `memo` in turn and returns how many values
    /// were made.
    fn request_keys(memo: &Memo<u8, u8>, keys: &[u8]) -> usize {
        let made = Cell::new(0);
        for &key in keys {
            memo.get(key, || {
                made.set(made.get() + 1);
                key
            });
        }
        made.get()
    }

    #[rstest]
    #[case::one_key_once(&[1, 1, 1], 1)]
    #[case::each_key_once(&[1, 2, 1, 2], 2)]
    #[case::past_the_cap_again(&[1, 2, 3, 1], 4)]
    fn makes_each_key_once_while_it_is_kept(#[case] keys: &[u8], #[case] made: usize) {
        assert_eq!(request_keys(&Memo::new(2), keys), made);
    }

    #[rstest]
    fn gives_the_value_made_for_the_key() {
        let memo = Memo::new(2);
        memo.get(1, || 10);
        assert_eq!((*memo.get(1, || 0), *memo.get(2, || 20)), (10, 20));
    }
}
