//! Strongly connected components, found with Tarjan's algorithm. Every
//! cycle lies inside one component, and every component of more than one
//! node contains a cycle. So the components give the loops without
//! listing every cycle, of which a component of a dozen nodes can have
//! thousands.

use std::collections::{BTreeMap, BTreeSet};

/// The components of the graph in which each key has an edge to every
/// node in its set. Each component is sorted, and the components come in
/// the order Tarjan's algorithm finishes them. A node that only appears
/// as a target, never as a key, is a component of its own.
pub fn components<N: Ord + Clone>(edges: &BTreeMap<N, BTreeSet<N>>) -> Vec<Vec<N>> {
    let mut search = Search {
        edges,
        index: BTreeMap::new(),
        low: BTreeMap::new(),
        stack: Vec::new(),
        on_stack: BTreeSet::new(),
        found: Vec::new(),
    };
    for node in edges.keys() {
        if !search.index.contains_key(node) {
            search.visit(node);
        }
    }
    search.found
}

struct Search<'a, N> {
    edges: &'a BTreeMap<N, BTreeSet<N>>,
    /// The order each node was first visited in.
    index: BTreeMap<&'a N, usize>,
    /// The lowest index each node can reach through nodes still on the
    /// stack. A node whose low index equals its own index is the root of
    /// a component.
    low: BTreeMap<&'a N, usize>,
    stack: Vec<&'a N>,
    on_stack: BTreeSet<&'a N>,
    found: Vec<Vec<N>>,
}

impl<'a, N: Ord + Clone> Search<'a, N> {
    /// Each call recurses into the nodes it reaches first, so the
    /// recursion can be as deep as the number of nodes in the graph.
    fn visit(&mut self, node: &'a N) {
        let order = self.index.len();
        self.index.insert(node, order);
        self.low.insert(node, order);
        self.stack.push(node);
        self.on_stack.insert(node);
        for next in self.edges.get(node).into_iter().flatten() {
            if !self.index.contains_key(next) {
                self.visit(next);
                let reached = self.low[next].min(self.low[node]);
                self.low.insert(node, reached);
            } else if self.on_stack.contains(next) {
                let reached = self.index[next].min(self.low[node]);
                self.low.insert(node, reached);
            }
        }
        if self.low[node] == self.index[node] {
            let mut component = Vec::new();
            while let Some(top) = self.stack.pop() {
                self.on_stack.remove(top);
                component.push(top.clone());
                if top == node {
                    break;
                }
            }
            component.sort();
            self.found.push(component);
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn graph(
        edges: &[(&'static str, &'static str)],
    ) -> BTreeMap<&'static str, BTreeSet<&'static str>> {
        let mut graph: BTreeMap<_, BTreeSet<_>> = BTreeMap::new();
        for &(from, to) in edges {
            graph.entry(from).or_default().insert(to);
        }
        graph
    }

    /// The components of more than one node, sorted, so each case lists
    /// its loops.
    fn loops(edges: &[(&'static str, &'static str)]) -> Vec<Vec<&'static str>> {
        let mut found: Vec<Vec<&str>> = components(&graph(edges))
            .into_iter()
            .filter(|component| component.len() > 1)
            .collect();
        found.sort();
        found
    }

    #[rstest]
    #[case::none(&[("a", "b"), ("b", "c")], vec![])]
    #[case::two(&[("a", "b"), ("b", "a")], vec![vec!["a", "b"]])]
    #[case::three_in_a_ring(&[("a", "b"), ("b", "c"), ("c", "a")], vec![vec!["a", "b", "c"]])]
    #[case::a_tail_into_a_loop(&[("x", "a"), ("a", "b"), ("b", "a")], vec![vec!["a", "b"]])]
    #[case::two_apart(
        &[("a", "b"), ("b", "a"), ("c", "d"), ("d", "c"), ("b", "c")],
        vec![vec!["a", "b"], vec!["c", "d"]]
    )]
    #[case::two_joined_both_ways(
        &[("a", "b"), ("b", "a"), ("c", "d"), ("d", "c"), ("b", "c"), ("d", "a")],
        vec![vec!["a", "b", "c", "d"]]
    )]
    #[case::a_self_edge(&[("a", "a")], vec![])]
    fn finds_each_set_of_nodes_that_reach_each_other(
        #[case] edges: &[(&'static str, &'static str)],
        #[case] expected: Vec<Vec<&'static str>>,
    ) {
        assert_eq!(loops(edges), expected);
    }

    #[rstest]
    fn a_node_that_is_only_a_target_is_its_own_component() {
        let found = components(&graph(&[("a", "b")]));
        assert_eq!(found, vec![vec!["b"], vec!["a"]]);
    }
}
