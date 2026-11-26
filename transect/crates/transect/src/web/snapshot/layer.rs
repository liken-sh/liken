//! Finds the layer of the map that a namespace is in, without building
//! the map.

use super::Snapshot;
use transect_graph::edges::Object;
use transect_map::map::UNSORTED;
use transect_resources::knowledge::{self, Named};
use transect_resources::layers::Layers;

impl Snapshot {
    /// The name of the layer, drawn as a band on the map, that
    /// `namespace` is in. Returns `None` when the cluster has no such
    /// namespace. Only the custom resources in `namespace` can change its
    /// layer, so this reads only those, where building the map reads all
    /// of them.
    pub fn layer_of(&self, namespace: &str, layers: &Layers) -> Option<String> {
        let meta = self.namespaces.iter().map(|found| &found.metadata);
        let meta = meta
            .into_iter()
            .find(|meta| meta.name.as_deref() == Some(namespace))?;
        let objects = self.custom.iter().flat_map(|(resource, objects)| {
            let here = objects.iter().filter_map(|object| {
                let meta = &object.metadata;
                let name = meta.name.as_deref()?;
                (meta.namespace.as_deref() == Some(namespace)).then_some(name)
            });
            here.map(|name| Object::new(&resource.group, &resource.kind, Some(namespace), name))
        });
        let objects: Vec<Object> = objects.collect();
        let named = knowledge::named(&self.kinds, &objects, layers, &self.plurals);
        let known = Named::layers(&named);
        let at = layers.place(meta, known.get(namespace).map(String::as_str));
        let name = match at {
            Some(at) => layers.names().nth(at)?,
            None => UNSORTED,
        };
        Some(name.to_string())
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::fixture;

    #[rstest]
    fn places_every_namespace_where_the_map_does() {
        let snapshot = fixture::snapshot();
        let layers = &snapshot.resources.layers;
        let map = snapshot.map(layers, Default::default());
        let names = snapshot
            .namespaces
            .iter()
            .filter_map(|n| n.metadata.name.clone());
        let (ours, maps): (Vec<_>, Vec<_>) = names
            .map(|name| {
                let ours = snapshot.layer_of(&name, layers);
                let maps = map.layer_of(&name).map(str::to_string);
                (ours, maps)
            })
            .unzip();
        assert_eq!(ours.len(), 8, "the fixture has 8 namespaces");
        assert_eq!(ours, maps);
    }

    #[rstest]
    fn has_no_layer_for_a_namespace_the_cluster_does_not_have() {
        let snapshot = fixture::snapshot();
        assert_eq!(
            snapshot.layer_of("nothing", &snapshot.resources.layers),
            None
        );
    }
}
