//! What kind knowledge gives one snapshot: the custom resources of
//! known kinds, the counts of those that run nothing, the layer a
//! namespace's kinds name, and what each resource's fields name.

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
    /// The custom kind `group`/`kind`, with the objects its store holds.
    pub fn custom_kind(&self, group: &str, kind: &str) -> Option<&(ApiResource, Vec<Arc<Custom>>)> {
        let mut custom = self.custom.iter();
        custom.find(|(api, _)| api.group == group && api.kind == kind)
    }

    /// The custom kind `resource` names in a URL, as `<plural>.<group>`,
    /// with the objects its store holds.
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

    /// The objects of known kinds in `namespace` that run nothing, one
    /// count per kind.
    pub fn counts(&self, namespace: &str) -> Vec<Count> {
        let workloads = self.ownership.workloads(namespace).collect();
        let objects = self.custom_objects();
        knowledge::counts(&self.kinds, &objects, &workloads, namespace, &self.plurals)
    }

    /// The custom resources in `namespace` that are not its workloads,
    /// one group per kind, for its page's Resources list.
    pub fn resources_in(&self, namespace: &str) -> Vec<Group> {
        let workloads = self.ownership.workloads(namespace).collect();
        let pages = self.pages();
        let href = |object: &Object| pages.href(object);
        let sources = objects::Sources {
            custom: &self.custom,
            kinds: &self.kinds,
            href: &href,
        };
        sources.groups(namespace, &workloads)
    }

    /// The layer `namespace`'s kinds' knowledge names, when neither its
    /// annotation nor a selector of `layers` places it.
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

    /// The reason each custom workload's own condition gives, by its
    /// namespace: the condition its kind knowledge names, or `Ready`.
    /// Only a workload draws a box for its health to color, so the
    /// resources of known kinds that run nothing are left out.
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
                    transect_observe::health::condition(&self.kinds, &object, found)
                {
                    reasons.push((namespace.clone(), reason));
                }
            }
        }
        reasons
    }

    /// The pod that takes writes for each custom resource whose kind
    /// knowledge names a `primary`, by the pod's name.
    pub fn primaries(&self) -> HashMap<Object, String> {
        let mut primaries = HashMap::new();
        for (resource, objects) in &self.custom {
            let known = self.kinds.of(&resource.group, &resource.kind);
            let Some(path) = known.and_then(|known| known.primary.as_deref()) else {
                continue;
            };
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let namespace = meta.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                let read = knowledge::path::read_of(&object, &found.data, path);
                if let Some(pod) = read.into_iter().next() {
                    primaries.insert(object, pod);
                }
            }
        }
        primaries
    }

    /// What each custom resource of a known kind names through its
    /// kind's knowledge, by the resource.
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

    /// The edges device knowledge draws between devices, and from a
    /// device to the object whose field holds one of its attributes.
    pub(super) fn device_edges(&self, reading: &StatedReading) -> Vec<Edge> {
        let knowledge = &self.resources.devices;
        let wanted: Vec<_> = transect_ground::devices::device_matched(knowledge).collect();
        let matches = knowledge::Matches::build(wanted, &self.custom);
        let stands_for = |object: &Object| reading.stands_for(object);
        transect_ground::devices::device_edges(&self.devices, knowledge, &matches, stands_for)
    }

    /// The edges kind knowledge's references draw, resolved against
    /// `graph` and `reading` as intent's entries are, leaving out what
    /// `graph` already holds from the API.
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

    /// The edges objects of known kinds give the applications behind the
    /// objects whose annotations name them, such as the identity
    /// provider a middleware's forward auth reaches, for each
    /// application behind an `Ingress` that lists the middleware.
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

    /// The `Secret` each custom resource of a known kind writes, by the
    /// resource, from its knowledge's `writesSecret`.
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

    /// How the intent for each `Secret` is read: from what writes it, and
    /// from its own annotations when `transect` watches them.
    pub fn secret_intent(&self) -> SecretIntent {
        SecretIntent::new(self.secrets.is_some(), &self.secrets_written())
    }

    /// The edges guessed from the field names of each custom resource
    /// whose kind has no knowledge.  Pods and claims count among what a
    /// field can name, as `known`'s objects do.
    pub(super) fn guessed_edges(
        &self,
        graph: &Graph,
        reading: &StatedReading,
        known: &HashSet<Object>,
    ) -> Vec<Edge> {
        let watched = self.guessable(known);
        let lookup = self.lookup(&watched, reading);
        let mut edges = Vec::new();
        // A trimmed kind keeps only its conditions, whose messages name
        // nothing a guess should follow, so only the kinds watched whole
        // are walked.  Walking every trimmed object would take seconds
        // on each change on a cluster with a few thousand of them.
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

    /// What a guess may name: `known`'s objects, the claims, and the
    /// pods.
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

    /// Each relation `object`'s fields hold, with what it reaches on
    /// `graph`, for its board.
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

/// The applications behind `referrer`: the workloads behind each
/// `Service` an `Ingress` routes to, or what any other object stands
/// for.
fn applications(graph: &Graph, reading: &StatedReading, referrer: &Object) -> Vec<Object> {
    let routed = graph.leaving(Kind::RoutesTo, &Endpoint::Object(referrer.clone()));
    let ends: Vec<Endpoint> = match routed.is_empty() {
        true => reading.stands_for(referrer),
        false => routed
            .into_iter()
            .filter_map(|end| match end {
                Endpoint::Object(service) => Some(reading.stands_for(service)),
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
