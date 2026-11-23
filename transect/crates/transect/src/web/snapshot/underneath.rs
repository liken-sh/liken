//! The object that describes the computer under a `Node`, such as the
//! record that a machine operator keeps for it. Its kind's knowledge
//! sets `node` to the path of the field that has the `Node` name. A
//! `Node` name is unique in the cluster, so the kind may be namespaced,
//! as Cluster API's `Machine` is. The machine page shows this object,
//! and search finds the machine by it.

use std::collections::HashSet;
use std::sync::Arc;

use super::Snapshot;
use transect_cluster::cluster::Custom;
use transect_graph::edges::Object;
use transect_resources::knowledge::path;

impl Snapshot {
    /// The first object whose `node` field has the `Node` name `name`.
    pub fn underneath(&self, name: &str) -> Option<(Object, &Arc<Custom>)> {
        self.describing()
            .find(|(_, _, nodes)| nodes.iter().any(|node| node == name))
            .map(|(object, found, _)| (object, found))
    }

    /// Every object that describes one of the cluster's `Node` objects.
    pub fn describing_machines(&self) -> HashSet<Object> {
        let machines: HashSet<&str> = self
            .nodes
            .iter()
            .filter_map(|node| node.metadata.name.as_deref())
            .collect();
        self.describing()
            .filter(|(_, _, nodes)| nodes.iter().any(|node| machines.contains(node.as_str())))
            .map(|(object, _, _)| object)
            .collect()
    }

    /// The group and kind of each kind whose knowledge sets `node`.
    pub fn describing_kinds(&self) -> impl Iterator<Item = &(String, String)> {
        self.kinds
            .kinds()
            .filter(|(group, kind)| self.node_path(group, kind).is_some())
    }

    fn node_path(&self, group: &str, kind: &str) -> Option<&str> {
        self.kinds.of(group, kind)?.node.as_deref()
    }

    /// Every object of a kind whose knowledge sets `node`, with the
    /// `Node` names in its `node` field.
    fn describing(&self) -> impl Iterator<Item = (Object, &Arc<Custom>, Vec<String>)> {
        self.custom.iter().flat_map(move |(resource, objects)| {
            let path = self.node_path(&resource.group, &resource.kind);
            objects.iter().filter_map(move |found| {
                let path = path?;
                let name = found.metadata.name.as_deref()?;
                let namespace = found.metadata.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let nodes = path::read_of(&object, &found.data, path);
                Some((object, found, nodes))
            })
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::fixture;
    use super::*;
    use transect_resources::knowledge::Kinds;

    /// The fixture, without the knowledge called `without`.
    fn snapshot(without: Option<&str>) -> Snapshot {
        let mut snapshot = fixture::snapshot();
        let kept: Vec<_> = snapshot
            .resources
            .kinds
            .iter()
            .filter(|known| Some(known.name.as_str()) != without)
            .cloned()
            .collect();
        let layers: Vec<&str> = snapshot.resources.layers.names().collect();
        snapshot.kinds = Kinds::resolve(&kept, &snapshot.plurals, &layers);
        snapshot
    }

    #[rstest]
    #[case::named_for_its_node(None, "node-alpha", Some("Machine node-alpha"))]
    #[case::a_node_nothing_describes(None, "node-beta", None)]
    #[case::without_the_knowledge(Some("machines.liken.sh"), "node-alpha", None)]
    fn finds_what_describes_a_machine(
        #[case] without: Option<&str>,
        #[case] node: &str,
        #[case] expected: Option<&str>,
    ) {
        let snapshot = snapshot(without);
        let found = snapshot.underneath(node);
        let found = found.map(|(object, _)| format!("{} {}", object.kind, object.name));
        assert_eq!(found.as_deref(), expected);
    }

    /// A cluster with one `Node` and a namespaced kind whose objects
    /// have the `Node` name in `status.nodeRef.name`.
    fn namespaced() -> Snapshot {
        let mut snapshot = Snapshot::empty();
        snapshot.nodes = vec![Arc::new(
            serde_yaml_ng::from_str("{metadata: {name: node-beta}}").unwrap(),
        )];
        let resource = kube::api::ApiResource {
            group: "hosts.example.test".into(),
            version: "v1".into(),
            api_version: "hosts.example.test/v1".into(),
            kind: "Host".into(),
            plural: "hosts".into(),
        };
        let host: Custom = serde_yaml_ng::from_str(
            "{apiVersion: hosts.example.test/v1, kind: Host, metadata: {namespace: racks, name: rack-2}, status: {nodeRef: {name: node-beta}}}",
        )
        .unwrap();
        snapshot.custom = vec![(resource, vec![Arc::new(host)])];
        snapshot
            .plurals
            .insert(("hosts.example.test".into(), "Host".into()), "hosts".into());
        let known = transect_resources::knowledge::fixtures::knowledge(
            "hosts.hosts.example.test",
            "{node: status.nodeRef.name}",
        );
        snapshot.kinds = Kinds::resolve(&[known], &snapshot.plurals, &[]);
        snapshot
    }

    #[rstest]
    fn finds_a_namespaced_object_that_describes_a_machine() {
        let snapshot = namespaced();
        let found = snapshot.underneath("node-beta").map(|(object, _)| object);
        assert_eq!(
            found,
            Some(Object::new(
                "hosts.example.test",
                "Host",
                Some("racks"),
                "rack-2"
            ))
        );
    }

    #[rstest]
    #[case::with_the_knowledge(None, 1)]
    #[case::without_it(Some("machines.liken.sh"), 0)]
    fn counts_the_objects_that_describe_a_node(
        #[case] without: Option<&str>,
        #[case] count: usize,
    ) {
        assert_eq!(snapshot(without).describing_machines().len(), count);
    }
}
