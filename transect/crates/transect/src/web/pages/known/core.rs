//! A `Pod` or a `Service` as a part of a sketch. A pattern of the
//! catalog draws each instance of a store as its `Pod` and each way into
//! it as a `Service`. No custom resource definition declares their
//! printer columns, so a part reads them under the names that
//! `kubectl get pods -o wide` and `kubectl get services` print, and a
//! `Pod` stands as the rules of `transect-observe` mark it on every
//! page. A reading that names an object, such as a claim or a pod
//! behind an endpoint, is a reading of its own, linked to the object.

use k8s_openapi::api::core::v1::{Pod, Service};
use k8s_openapi::jiff::Timestamp;
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_map::map::{Reading, Standing, Why};
use transect_network::entry::{Endpoints, ServiceBox};
use transect_observe::health::{self, Health, Level};
use transect_resources::knowledge::age;
use transect_resources::knowledge::role::Column;

use super::super::super::snapshot::Snapshot;
use super::super::object::typed;

/// A part of a kind of the core group that a sketch reads by the
/// columns that `kubectl get` prints, with the object that the part
/// names, or none when the cluster has no such object.
enum Core<'s> {
    Pod(Option<&'s Pod>),
    Service(Option<&'s Service>),
}

/// The part that `object` names, when its kind is one that this module
/// reads.
fn core<'s>(snapshot: &'s Snapshot, object: &Object) -> Option<Core<'s>> {
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Pod") => Some(Core::Pod(typed(&snapshot.pods, object))),
        ("", "Service") => Some(Core::Service(typed(&snapshot.services, object))),
        _ => None,
    }
}

/// How the `Pod` that `object` names stands: missing when the cluster
/// has no such `Pod`, and otherwise as the rules that mark a `Pod` on
/// every page say, with the first reason as the part's line, in brief
/// because the part names its pod, and in full when the reader points
/// at it. It is none when `object` is not a `Pod`.
pub(super) fn standing(snapshot: &Snapshot, object: &Object, now: Timestamp) -> Option<Standing> {
    let Core::Pod(pod) = core(snapshot, object)? else {
        return None;
    };
    let Some(pod) = pod else {
        return Some(Standing::Missing);
    };
    let health = Health::from_reasons(health::pod_reasons(pod, now));
    let why = health.reasons.first().map(|reason| Why {
        reason: reason.brief(),
        message: Some(reason.to_string()),
    });
    Some(match (health.level, why) {
        (Level::Failing, Some(why)) => Standing::Failing(why),
        (Level::Warning, Some(why)) => Standing::Warning(why),
        _ => Standing::Plain,
    })
}

/// Each of `columns` that has a value on the `Pod` or the `Service` that
/// `object` names, in their order, or none when `object` is neither.
/// `endpoints` are the page's, built once for every part.
pub(super) fn readings(
    snapshot: &Snapshot,
    endpoints: &Endpoints,
    object: &Object,
    columns: &[Column],
    now: Timestamp,
) -> Option<Vec<Reading>> {
    let read = match core(snapshot, object)? {
        Core::Pod(Some(pod)) => columns
            .iter()
            .flat_map(|column| pod_column(snapshot, pod, &column.name, now))
            .collect(),
        Core::Service(Some(service)) => {
            let shown = ServiceBox::build(service, endpoints);
            let pods = endpoints.ready_pods(&shown.namespace, &shown.name);
            let read = columns
                .iter()
                .flat_map(|column| match column.name.as_str() {
                    "Ports" if !shown.ports.is_empty() => {
                        vec![plain("Ports", shown.ports.join(","))]
                    }
                    "Pods" => pods
                        .iter()
                        .map(|pod| behind(snapshot, &shown, pod))
                        .collect(),
                    _ => Vec::new(),
                });
            read.collect()
        }
        Core::Pod(None) | Core::Service(None) => Vec::new(),
    };
    Some(read)
}

/// The reading `Pods` for `pod`, behind a ready endpoint of `shown`,
/// linked to the pod's page.
fn behind(snapshot: &Snapshot, shown: &ServiceBox, pod: &str) -> Reading {
    let object = Object::new("", "Pod", Some(&shown.namespace), pod);
    Reading {
        name: "Pods".into(),
        value: pod.into(),
        from: Some(Reference {
            text: format!("Pod {pod}, behind a ready endpoint of {}", shown.name),
            href: transect_graph::links::page(&object, &snapshot.plurals),
        }),
    }
}

/// The values of the column `name` of `pod`. `Claim` has one value for
/// each claim that the pod mounts, with its size, linked to the claim.
fn pod_column(snapshot: &Snapshot, pod: &Pod, name: &str, now: Timestamp) -> Vec<Reading> {
    let spec = pod.spec.as_ref();
    match name {
        "Ready" => {
            let (ready, total) = transect_graph::pods::ready_containers(pod);
            vec![plain(name, format!("{ready}/{total}"))]
        }
        "Node" => {
            let node = spec.and_then(|spec| spec.node_name.clone());
            node.into_iter().map(|node| plain(name, node)).collect()
        }
        "Age" => {
            let created = pod.metadata.creation_timestamp.as_ref();
            let minutes = created.map(|created| health::minutes_between(created.0, now));
            minutes
                .into_iter()
                .map(|minutes| plain(name, age(minutes)))
                .collect()
        }
        "Claim" => {
            let volumes = spec.and_then(|spec| spec.volumes.as_ref());
            let claims = volumes.into_iter().flatten();
            let claims = claims.filter_map(|volume| volume.persistent_volume_claim.as_ref());
            let namespace = pod.metadata.namespace.as_deref();
            let read = claims.map(|claim| {
                let object = Object::new("", "PersistentVolumeClaim", namespace, &claim.claim_name);
                let size = typed(&snapshot.paged.claims, &object);
                let size = size.and_then(|c| c.status.as_ref()?.capacity.as_ref()?.get("storage"));
                let value = match size {
                    Some(size) => format!("{} {}", claim.claim_name, size.0),
                    None => claim.claim_name.clone(),
                };
                Reading {
                    name: name.into(),
                    value,
                    from: Some(Reference {
                        text: format!(
                            "PersistentVolumeClaim {}, which spec.volumes names",
                            claim.claim_name
                        ),
                        href: transect_graph::links::page(&object, &snapshot.plurals),
                    }),
                }
            });
            read.collect()
        }
        _ => Vec::new(),
    }
}

fn plain(name: &str, value: String) -> Reading {
    Reading {
        name: name.into(),
        value,
        from: None,
    }
}

/// Whether the `Pod` that `pod` names is ready, or none when the cluster
/// has no such `Pod`.
pub(super) fn ready(snapshot: &Snapshot, pod: &Object) -> Option<bool> {
    match core(snapshot, pod)? {
        Core::Pod(found) => found.map(transect_graph::pods::ready),
        Core::Service(_) => None,
    }
}

/// The pods behind the ready endpoints of the `Service` that `service`
/// names, or none when the cluster has no such `Service`.
pub(super) fn endpoints(
    snapshot: &Snapshot,
    endpoints: &Endpoints,
    service: &Object,
) -> Option<Vec<Object>> {
    let Core::Service(Some(_)) = core(snapshot, service)? else {
        return None;
    };
    let namespace = service.namespace.as_deref();
    let ready = endpoints.ready_pods(namespace.unwrap_or_default(), &service.name);
    let pods = ready
        .iter()
        .map(|name| Object::new("", "Pod", namespace, name));
    Some(pods.collect())
}
