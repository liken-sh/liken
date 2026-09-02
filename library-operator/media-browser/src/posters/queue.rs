// The queue serves the newest request first. A fast scroll then fills
// the posters under the focus before the ones it scrolled past.
//
// Page-size decodes hold one lane of their own: at most one is in flight,
// and a slot-size request passes a page-size one that waits. A page-size
// decode reads a source of up to several megapixels, and four of them at
// once dirtied four arenas on a one-gigabyte box.

use std::collections::HashSet;
use std::hash::Hash;

// One queued request and the lane it belongs to.
struct Queued<K> {
    key: K,
    page: bool,
}

pub(crate) struct RequestQueue<K> {
    stack: Vec<Queued<K>>,
    in_flight: HashSet<K>,
    // The page-size key a worker is decoding. It blocks the page lane
    // until finish clears it.
    page_in_flight: Option<K>,
}

impl<K> Default for RequestQueue<K> {
    fn default() -> Self {
        Self {
            stack: Vec::new(),
            in_flight: HashSet::new(),
            page_in_flight: None,
        }
    }
}

impl<K: Clone + Eq + Hash> RequestQueue<K> {
    // A key already queued moves to the top, a key already decoding is
    // left alone, and only a new key returns true to ask for a worker.
    pub(crate) fn request(&mut self, key: K, page: bool) -> bool {
        if self.in_flight.contains(&key) {
            if let Some(at) = self.stack.iter().position(|queued| queued.key == key) {
                let queued = self.stack.remove(at);
                self.stack.push(queued);
            }
            return false;
        }
        self.in_flight.insert(key.clone());
        self.stack.push(Queued { key, page });
        true
    }

    // The key stays in flight until finish, so a repeat request during
    // the decode does not queue it twice.
    //
    // The newest takeable request wins. A page-size request is not
    // takeable while another page-size decode runs.
    pub(crate) fn take(&mut self) -> Option<K> {
        let lane_free = self.page_in_flight.is_none();
        let at = self
            .stack
            .iter()
            .rposition(|queued| lane_free || !queued.page)?;
        let queued = self.stack.remove(at);
        if queued.page {
            self.page_in_flight = Some(queued.key.clone());
        }
        Some(queued.key)
    }

    pub(crate) fn finish(&mut self, key: &K) {
        self.in_flight.remove(key);
        if self.page_in_flight.as_ref() == Some(key) {
            self.page_in_flight = None;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::RequestQueue;

    #[test]
    fn serves_the_newest_request_first() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a", false));
        assert!(queue.request("b", false));
        assert!(queue.request("c", false));
        assert_eq!(queue.take(), Some("c"));
        assert_eq!(queue.take(), Some("b"));
        assert_eq!(queue.take(), Some("a"));
        assert_eq!(queue.take(), None);
    }

    #[test]
    fn a_repeat_request_moves_to_the_top() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a", false));
        assert!(queue.request("b", false));
        assert!(!queue.request("a", false));
        assert_eq!(queue.take(), Some("a"));
        assert_eq!(queue.take(), Some("b"));
        assert_eq!(queue.take(), None);
    }

    #[test]
    fn a_key_being_decoded_is_not_queued_again() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("a", false));
        assert_eq!(queue.take(), Some("a"));
        assert!(!queue.request("a", false));
        assert_eq!(queue.take(), None);
        queue.finish(&"a");
        assert!(queue.request("a", false));
        assert_eq!(queue.take(), Some("a"));
    }

    #[test]
    fn one_page_size_request_is_in_flight_at_a_time() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("first", true));
        assert!(queue.request("second", true));
        assert_eq!(queue.take(), Some("second"));
        assert_eq!(queue.take(), None);
        queue.finish(&"second");
        assert_eq!(queue.take(), Some("first"));
        assert_eq!(queue.take(), None);
    }

    #[test]
    fn a_slot_request_passes_a_waiting_page_size_one() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("slot", false));
        assert!(queue.request("page", true));
        assert!(queue.request("newer page", true));
        assert_eq!(queue.take(), Some("newer page"));
        assert_eq!(queue.take(), Some("slot"));
        assert_eq!(queue.take(), None);
        queue.finish(&"newer page");
        assert_eq!(queue.take(), Some("page"));
    }

    #[test]
    fn a_finished_slot_leaves_the_page_lane_alone() {
        let mut queue = RequestQueue::default();
        assert!(queue.request("page", true));
        assert!(queue.request("slot", false));
        assert_eq!(queue.take(), Some("slot"));
        assert_eq!(queue.take(), Some("page"));
        queue.finish(&"slot");
        assert!(queue.request("other page", true));
        assert_eq!(queue.take(), None);
    }
}
