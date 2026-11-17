//! What a model derives on a render's first request, kept for the
//! renders that request the same value after it.  A model is kept until
//! the next change, and what it holds is dropped with it.

use std::collections::HashMap;
use std::hash::Hash;
use std::sync::{Arc, Mutex};

/// The values made for up to `cap` keys.  A key past the cap clears the
/// rest, so a reader trying many edge sets keeps no more than a few.
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

    /// The value kept for `key`, or the one `make` gives, kept.  `make`
    /// runs outside the lock, so a render requesting another key need not
    /// wait for it; two renders requesting one new key together may both
    /// make it, and the first one kept wins.
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

    /// Asks `memo` for each key in turn, counting the values made.
    fn ask(memo: &Memo<u8, u8>, keys: &[u8]) -> usize {
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
        assert_eq!(ask(&Memo::new(2), keys), made);
    }

    #[rstest]
    fn gives_the_value_made_for_the_key() {
        let memo = Memo::new(2);
        memo.get(1, || 10);
        assert_eq!((*memo.get(1, || 0), *memo.get(2, || 20)), (10, 20));
    }
}
