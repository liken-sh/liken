//! Strongly connected components, by Tarjan's algorithm: the sets of
//! nodes in which every node reaches every other along the edges.  A
//! component of more than one node holds a cycle, and every cycle lies
//! inside one component, so the components give the loops without
//! listing each cycle, which can number in the thousands where a
//! component holds a dozen nodes.

use std::collections::{BTreeMap, BTreeSet};

/// The components of the graph whose edges leave each key for each node
/// in its set, each component sorted, in the order Tarjan's algorithm
/// closes them.  A node only reached, never a key, is its own component.
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
    /// The lowest index each node reaches through the nodes still on the
    /// stack; a node whose own index it is roots a component.
    low: BTreeMap<&'a N, usize>,
    stack: Vec<&'a N>,
    on_stack: BTreeSet<&'a N>,
    found: Vec<Vec<N>>,
}

impl<'a, N: Ord + Clone> Search<'a, N> {
    /// Recursion is as deep as the longest path, which on a graph of a
    /// cluster's namespaces is a few dozen at most.
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

    /// The components of more than one node, sorted, so a case reads
    /// as the loops it holds.
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
    fn a_node_only_reached_is_its_own_component() {
        let found = components(&graph(&[("a", "b")]));
        assert_eq!(found, vec![vec!["b"], vec!["a"]]);
    }
}
