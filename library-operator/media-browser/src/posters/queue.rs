// The queue serves the newest request first. A fast scroll then fills
// the posters under the focus before the ones it scrolled past.

use std::collections::HashSet;
use std::hash::Hash;

pub(crate) struct RequestQueue<K> {
    stack: Vec<K>,
    in_flight: HashSet<K>,
}

impl<K> Default for RequestQueue<K> {
    fn default() -> Self {
        Self {
            stack: Vec::new(),
            in_flight: HashSet::new(),
        }
    }
}

impl<K: Clone + Eq + Hash> RequestQueue<K> {
    // A key already queued moves to the top, a key already decoding is
    // left alone, and only a new key returns true to ask for a worker.
    pub(crate) fn request(&mut self, key: K) -> bool {
        if self.in_flight.contains(&key) {
            if let Some(at) = self.stack.iter().position(|queued| *queued == key) {
                let queued = self.stack.remove(at);
                self.stack.push(queued);
            }
            return false;
        }
        self.in_flight.insert(key.clone());
        self.stack.push(key);
        true
    }

    // The key stays in flight until finish, so a repeat request during
    // the decode does not queue it twice.
    pub(crate) fn take(&mut self) -> Option<K> {
        self.stack.pop()
    }

    pub(crate) fn finish(&mut self, key: &K) {
        self.in_flight.remove(key);
    }
}

#[cfg(test)]
mod tests {
    use super::RequestQueue;

    #[test]
    fn serves_the_newest_request_first() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a"));
        assert!(queue.request("b"));
        assert!(queue.request("c"));
        assert_eq!(queue.take(), Some("c"));
        assert_eq!(queue.take(), Some("b"));
        assert_eq!(queue.take(), Some("a"));
        assert_eq!(queue.take(), None);
    }

    #[test]
    fn a_repeat_request_moves_to_the_top() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a"));
        assert!(queue.request("b"));
        assert!(!queue.request("a"));
        assert_eq!(queue.take(), Some("a"));
        assert_eq!(queue.take(), Some("b"));
        assert_eq!(queue.take(), None);
    }

    #[test]
    fn a_key_being_decoded_is_not_queued_again() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a"));
        assert_eq!(queue.take(), Some("a"));
        assert!(!queue.request("a"));
        assert_eq!(queue.take(), None);
        queue.finish(&"a");
        assert!(queue.request("a"));
        assert_eq!(queue.take(), Some("a"));
    }
}
