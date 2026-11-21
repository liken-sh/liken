//! The parts of `Snapshot` that read custom resources through kind
//! knowledge. Kind knowledge is what a resource declares about a custom
//! kind: which condition means an object is healthy, which layer its
//! namespaces belong to, and which fields refer to other objects.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use kube::core::ApiResource;

use super::Snapshot;
use transect_cluster::cluster::Custom;
use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object};
use transect_map::objects::{self, Group};
use transect_network::addresses::guess::Lookup;
use transect_network::addresses::{Readers, StatedReading};
use transect_observe::health::Reason;
use transect_resources::intent::SecretIntent;
use transect_resources::knowledge::{self, Count, Named, Names, Reached, Relation};
use transect_resources::layers::Layers;

impl Snapshot {
    /// The custom kind `group`/`kind`, with its objects.
    pub fn custom_kind(&self, group: &str, kind: &str) -> Option<&(ApiResource, Vec<Arc<Custom>>)> {
        let mut custom = self.custom.iter();
        custom.find(|(api, _)| api.group == group && api.kind == kind)
    }

    /// The custom kind at the URL segment `resource`, which is written
    /// `<plural>.<group>`, with its objects.
    pub fn custom_at(&self, resource: &str) -> Option<&(ApiResource, Vec<Arc<Custom>>)> {
        let mut custom = self.custom.iter();
        custom.find(|(api, _)| {
            transect_graph::links::kinds::url_name(&api.group, &api.plural) == resource
        })
    }

    /// Every custom resource `transect` watches.
    pub fn custom_objects(&self) -> Vec<Object> {
        let objects = self.custom.iter().flat_map(|(resource, objects)| {
            objects.iter().filter_map(|object| {
                let meta = &object.metadata;
                let name = meta.name.as_deref()?;
                let namespace = meta.namespace.as_deref();
                Some(Object::new(
                    &resource.group,
                    &resource.kind,
                    namespace,
                    name,
                ))
            })
        });
        objects.collect()
    }

    /// For each known kind, how many of its objects in `namespace` run
    /// nothing.
    pub fn counts(&self, namespace: &str) -> Vec<Count> {
        let workloads = self.ownership.workloads(namespace).collect();
        let objects = self.custom_objects();
        knowledge::counts(&self.kinds, &objects, &workloads, namespace, &self.plurals)
    }

    /// The custom resources in `namespace` that are not workloads, one
    /// group per kind, for the namespace page's Resources list.
    pub fn resources_in(&self, namespace: &str) -> Vec<Group> {
        let workloads = self.ownership.workloads(namespace).collect();
        let pages = self.pages();
        let href = |object: &Object| pages.href(object);
        let sources = objects::Sources {
            custom: &self.custom,
            kinds: &self.kinds,
            ends: self,
            href: &href,
        };
        sources.groups(namespace, &workloads)
    }

    /// The layer that kind knowledge assigns to `namespace`, or `None`
    /// when the namespace's annotation or a selector in `layers` places
    /// it, because those take precedence.
    pub(super) fn layer_named(&self, namespace: &str, layers: &Layers) -> Option<Named> {
        let meta = self.namespaces.iter().map(|n| &n.metadata);
        let meta = meta
            .into_iter()
            .find(|meta| meta.name.as_deref() == Some(namespace))?;
        if layers.decides(meta) {
            return None;
        }
        let objects = self.custom_objects();
        let mut named = knowledge::named(&self.kinds, &objects, layers, &self.plurals);
        named.remove(namespace)
    }

    /// The health reason of each custom resource that is a workload,
    /// with its namespace, read from the condition that its kind
    /// knowledge names, or from `Ready`. Only a workload's box shows
    /// health, so other custom resources are skipped.
    pub(super) fn conditions_of_custom(&self) -> Vec<(String, Reason)> {
        let workloads: HashSet<&Object> = self.ownership.all_workloads().collect();
        let mut reasons = Vec::new();
        for (resource, objects) in &self.custom {
            for found in objects {
                let meta = &found.metadata;
                let (Some(namespace), Some(name)) = (&meta.namespace, &meta.name) else {
                    continue;
                };
                let object = Object::new(&resource.group, &resource.kind, Some(namespace), name);
                if !workloads.contains(&object) {
                    continue;
                }
                if let Some(reason) =
                    transect_observe::health::condition(&self.kinds, self, &object, found)
                {
                    reasons.push((namespace.clone(), reason));
                }
            }
        }
        reasons
    }

    /// Maps each custom resource to the name of its primary pod, the
    /// pod that accepts writes, read from the entry of `references` that
    /// fills the role of its kind's pattern that states `failover`.
    pub fn primaries(&self) -> HashMap<Object, String> {
        let mut primaries = HashMap::new();
        for (resource, objects) in &self.custom {
            let known = self.kinds.of(&resource.group, &resource.kind);
            let pattern = known.and_then(|known| known.pattern.as_ref());
            let Some(path) = pattern.and_then(|pattern| pattern.failover()) else {
                continue;
            };
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let namespace = meta.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let relations = self.relations.get(&object).into_iter().flatten();
                let mut read = relations.filter(|relation| relation.path == path);
                if let Some(pod) = read.next() {
                    primaries.insert(object, pod.value.clone());
                }
            }
        }
        primaries
    }

    /// Reads the references to other objects in the fields that each
    /// custom resource's kind knowledge names.
    pub(super) fn read_relations(&self) -> HashMap<Object, Vec<Relation>> {
        let mut relations = HashMap::new();
        let wanted = self
            .kinds
            .kinds()
            .filter_map(|(group, kind)| self.kinds.of(group, kind));
        let matches = knowledge::Matches::build(
            wanted.flat_map(|known| known.matched()).collect::<Vec<_>>(),
            &self.custom,
        );
        for (resource, objects) in &self.custom {
            let Some(known) = self.kinds.of(&resource.group, &resource.kind) else {
                continue;
            };
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let namespace = meta.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let scoped = &self.cluster_scoped;
                let read = knowledge::relations(known, &object, &found.data, scoped, &matches);
                relations.insert(object, read);
            }
        }
        relations
    }

    /// The edges that device knowledge defines: between devices, and
    /// from a device to the object whose field holds one of the
    /// device's attributes.
    pub(super) fn device_edges(&self, reading: &StatedReading) -> Vec<Edge> {
        let knowledge = &self.resources.devices;
        let wanted: Vec<_> = transect_ground::devices::device_matched(knowledge).collect();
        let matches = knowledge::Matches::build(wanted, &self.custom);
        let endpoints_of = |object: &Object| reading.endpoints_of(object);
        transect_ground::devices::device_edges(&self.devices, knowledge, &matches, endpoints_of)
    }

    /// The edges for the references that kind knowledge reads. Each
    /// reference is resolved the way intent entries are, and an edge
    /// that `graph` already has from the API is left out.
    pub(super) fn known_edges(&self, graph: &Graph, reading: &StatedReading) -> Vec<Edge> {
        let mut edges = Vec::new();
        for (object, relations) in &self.relations {
            let reached = |relation: &Relation| {
                transect_network::addresses::relations::reached(
                    relation,
                    object,
                    reading,
                    &self.devices,
                )
            };
            let owns = |end: &_| reading.owns(object, end);
            edges.extend(knowledge::edges(object, relations, reached, owns, graph));
        }
        edges
    }

    /// The edges that an object's annotation makes for other objects.
    /// For example, an `Ingress` lists a middleware whose forward auth
    /// reaches an identity provider, so each application behind that
    /// `Ingress` gets an edge to the identity provider.
    pub(super) fn referred_edges(&self, graph: &Graph, reading: &StatedReading) -> Vec<Edge> {
        let described = self.described();
        let referrals = knowledge::referrals(&self.kinds, &self.custom_objects(), &described);
        let reached = |target: &Object, relation: &Relation| {
            transect_network::addresses::relations::reached(
                relation,
                target,
                reading,
                &self.devices,
            )
        };
        let behind = |referrer: &Object| applications(graph, reading, referrer);
        let owns = |application: &Object, end: &Endpoint| reading.owns(application, end);
        knowledge::referred_edges(&referrals, &self.relations, reached, behind, owns, graph)
    }

    /// Maps each custom resource to the `Secret` that its kind
    /// knowledge's `writesSecret` names. A resource whose `writesSecret`
    /// does not resolve to one object is left out.
    pub(super) fn secrets_written(&self) -> HashMap<Object, Object> {
        let mut written = HashMap::new();
        for (resource, objects) in &self.custom {
            let Some(known) = self.kinds.of(&resource.group, &resource.kind) else {
                continue;
            };
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let namespace = meta.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let secret = knowledge::written_secret(known, &object, &found.data);
                if let Some(Names::Object(secret)) = secret.map(|relation| relation.names) {
                    written.insert(object, secret);
                }
            }
        }
        written
    }

    /// Where the intent for each `Secret` comes from: the resources
    /// that write it, and the `Secret`'s own annotations when
    /// `transect` watches `Secret`s.
    pub fn secret_intent(&self) -> SecretIntent {
        SecretIntent::new(self.secrets.is_some(), &self.secrets_written())
    }

    /// Guesses edges from the fields of each custom resource whose kind
    /// has no kind knowledge. A field can name an object in `known`, a
    /// claim, or a pod.
    pub(super) fn guessed_edges(
        &self,
        graph: &Graph,
        reading: &StatedReading,
        known: &HashSet<Object>,
    ) -> Vec<Edge> {
        let watched = self.guessable(known);
        let lookup = self.lookup(&watched, reading);
        let mut edges = Vec::new();
        // Only kinds watched whole are walked. A trimmed object keeps
        // only its metadata, conditions, and printer-column values, and
        // walking every trimmed object would be slow on a cluster with
        // thousands of them.
        let whole = self.custom.iter().filter(|(resource, _)| {
            let kind = (resource.group.clone(), resource.kind.clone());
            self.paged.whole.contains(&kind)
        });
        for (resource, objects) in whole {
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let namespace = meta.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let guessed = transect_network::addresses::guess::guesses(
                    &self.kinds,
                    &object,
                    &found.data,
                    &lookup,
                    graph,
                );
                for edge in &guessed {
                    tracing::debug!(from = ?edge.from, to = ?edge.to, provenance = ?edge.provenance, "guessed a reference");
                }
                edges.extend(guessed);
            }
        }
        edges
    }

    /// The objects a guessed reference can point to.
    fn guessable(&self, known: &HashSet<Object>) -> HashSet<Object> {
        let mut watched = known.clone();
        watched.extend(self.storage.claim_objects());
        let pods = self.pods.iter().filter_map(|pod| {
            let meta = &pod.metadata;
            let name = meta.name.as_deref()?;
            Some(Object::new("", "Pod", meta.namespace.as_deref(), name))
        });
        watched.extend(pods);
        watched
    }

    fn lookup<'a>(
        &'a self,
        watched: &'a HashSet<Object>,
        reading: &'a StatedReading,
    ) -> Lookup<'a> {
        Lookup {
            plurals: &self.plurals,
            scoped: &self.cluster_scoped,
            watched,
            reading,
        }
    }

    /// Each reference in `object`'s fields, with the endpoints it
    /// reaches in `graph`, for the object's page.
    pub fn relations_reached(&self, object: &Object, graph: &Graph) -> Vec<(&Relation, Reached)> {
        let known = self.known_objects();
        let readers = Readers::default();
        let reading = self.reading(graph, &known, &readers);
        let relations = self.relations.get(object).into_iter().flatten();
        relations
            .map(|relation| {
                let mut reached = transect_network::addresses::relations::reached(
                    relation,
                    object,
                    &reading,
                    &self.devices,
                );
                let ends = reached.ends.into_iter();
                reached.ends = ends.map(|end| self.outsides.fold_end(end)).collect();
                (relation, reached)
            })
            .collect()
    }
}

/// The applications behind `referrer`. For an object that routes to
/// `Service`s, such as an `Ingress`, they are the applications behind
/// each `Service`, as `reading.endpoints_of` returns them. Otherwise they
/// are what `reading.endpoints_of` returns for `referrer`.
fn applications(graph: &Graph, reading: &StatedReading, referrer: &Object) -> Vec<Object> {
    let routed = graph.leaving(Kind::RoutesTo, &Endpoint::Object(referrer.clone()));
    let ends: Vec<Endpoint> = match routed.is_empty() {
        true => reading.endpoints_of(referrer),
        false => routed
            .into_iter()
            .filter_map(|end| match end {
                Endpoint::Object(service) => Some(reading.endpoints_of(service)),
                _ => None,
            })
            .flatten()
            .collect(),
    };
    let mut found: Vec<Object> = Vec::new();
    for end in ends {
        if let Endpoint::Object(object) = end
            && !found.contains(&object)
        {
            found.push(object);
        }
    }
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::fixture;
    use transect_observe::health::Level;

    /// A vent is as healthy as the thermostat it names, so the
    /// namespace page draws the vent whose thermostat sleeps at rest,
    /// and the vent whose thermostat the cluster does not have as a
    /// warning.
    #[rstest]
    fn draws_a_health_through_a_reference_in_the_namespaces_list() {
        let groups = fixture::snapshot().resources_in("acme-ops");
        let vents = groups.iter().find(|group| group.label == "Vents").unwrap();
        let items = vents.items.iter();
        let items: Vec<(&str, Option<Level>, bool)> = items
            .map(|item| (item.name.as_str(), item.level, item.resting))
            .collect();
        assert_eq!(
            items,
            vec![
                ("east-vent", Some(Level::Healthy), true),
                ("west-vent", Some(Level::Warning), false),
            ]
        );
    }
}
