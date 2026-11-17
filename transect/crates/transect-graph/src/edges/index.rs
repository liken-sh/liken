//! The graph's edges looked up by one end.  Sources such as depends-on
//! query the edges that leave or reach one end for each address of each
//! workload, and the managed-by and runs-on edges alone number more than
//! the pods, so a scan of every edge per query grows with the number of
//! pairs of pods.
//!
//! An end is keyed by its hash, not by a copy of it, because copying every
//! end's names twice costs more than the scans it saves on a cluster of
//! a few thousand pods.  Two ends can share a hash, so the edges an
//! index gives are candidates, and the graph keeps those whose end is
//! the one the caller asked for.

use std::collections::HashMap;
use std::hash::{BuildHasher, RandomState};

use super::{Endpoint, Kind};

/// For each kind and end's hash, the edges at that end, as indexes into
/// the graph's edges in the order the sources gave them.
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

    /// The `kind` edges that may be at `end`, in order.
    pub fn candidates(&self, kind: Kind, end: &Endpoint) -> &[usize] {
        let key = (kind, self.hasher.hash_one(end));
        self.by_end.get(&key).map_or(&[], Vec::as_slice)
    }
}
