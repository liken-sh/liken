//! A lookup of the graph's edges by one end. Sources such as depends-on
//! query the edges that leave or reach one end, for each address of each
//! workload. The managed-by and runs-on edges alone outnumber the pods,
//! so scanning every edge for each query would take time proportional to
//! the number of pairs of pods.
//!
//! An end is keyed by its hash, so the index holds no copy of its
//! names. Two ends can have the same hash, so the index returns
//! candidates, and the graph keeps the ones whose end equals the one
//! requested.

use std::collections::HashMap;
use std::hash::{BuildHasher, RandomState};

use super::{Endpoint, Kind};

/// For each kind and end hash, the edges at that end, as indexes into
/// the graph's edges in the order the sources added them.
#[derive(Clone, Debug, Default)]
pub struct Index {
    hasher: RandomState,
    by_end: HashMap<(Kind, u64), Vec<usize>>,
}

impl Index {
    pub fn add(&mut self, kind: Kind, end: &Endpoint, edge: usize) {
        let key = (kind, self.hasher.hash_one(end));
        self.by_end.entry(key).or_default().push(edge);
    }

    /// The indexes of the `kind` edges that may be at `end`, in the order
    /// they were added. Some may be at a different end with the same hash.
    pub fn candidates(&self, kind: Kind, end: &Endpoint) -> &[usize] {
        let key = (kind, self.hasher.hash_one(end));
        self.by_end.get(&key).map_or(&[], Vec::as_slice)
    }
}
