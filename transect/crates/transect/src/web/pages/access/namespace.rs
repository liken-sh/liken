//! A namespace's access tab: the ways in from outside the cluster, what
//! each account may do in the namespace, the `NetworkPolicy` objects
//! that limit each workload, and the namespace's Pod Security level.

use k8s_openapi::jiff::Timestamp;

use super::look::Look;
use crate::web::model::Model;
use transect_access::access::accounts::{Column, Named, namespace_rows};
use transect_access::access::namespace::{NamespaceAccessPage, Served, Way, pod_security};
use transect_access::access::ways::page::Dot;
use transect_access::access::ways::{self, Gate, State, Through};
use transect_access::access::{ALL, Look as RowLook};
use transect_graph::edges::{Object, Shown};
use transect_network::policy::Reach;
use transect_resources::layers::Layers;

/// The label that sets the Pod Security level a namespace enforces.
const ENFORCE: &str = "pod-security.kubernetes.io/enforce";

/// The access tab of `namespace`, or none when the cluster has no such
/// namespace.
pub fn namespace_access_page(
    model: &Model,
    layers: &Layers,
    namespace: &str,
    now: Timestamp,
) -> Option<NamespaceAccessPage> {
    let snapshot = &model.snapshot;
    let found = snapshot
        .namespaces
        .iter()
        .find(|found| found.metadata.name.as_deref() == Some(namespace))?;
    let layer = snapshot.layer_of(namespace, layers)?;
    let look = Look::new(model);
    let href = |object: &Object| look.href(object);
    let why = |object: &Object| snapshot.intents.of(object)?.why.clone();
    let rows = RowLook {
        href: &href,
        why: &why,
    };
    let (accounts, controllers) = namespace_rows(&model.access, namespace, &rows);
    let columns = ALL
        .iter()
        .map(|capability| Column {
            name: capability.words(),
            verbs: capability.verbs(&model.access.gitops),
        })
        .collect();
    let labels = found.metadata.labels.as_ref();
    let level = labels
        .and_then(|labels| labels.get(ENFORCE))
        .map(String::as_str);
    let minute = model.minute(now);
    let mut health = minute.health.clone();
    let shown = Shown::default();
    let graph = model.graph(&shown);
    let edge_set = model.edge_set(&shown);
    let kept = (&edge_set.forges, &minute.logged[..]);
    let card = snapshot.card(
        namespace,
        health.remove(namespace).unwrap_or_default(),
        &graph,
        kept,
        model.allowed(&shown),
    );
    let card = card.with_loops(&edge_set.loops);
    let page = NamespaceAccessPage {
        namespace: namespace.into(),
        layer,
        served: served(model, namespace, &look),
        columns,
        accounts,
        controllers,
        pod_security: pod_security(namespace, level),
        card,
    };
    Some(page)
}

/// One entry for each workload in `namespace` that serves a port,
/// sorted by name and kind, with the ways in that reach it.
fn served(model: &Model, namespace: &str, look: &Look) -> Vec<Served> {
    let snapshot = &model.snapshot;
    let ways = model.ways();
    let allowed = model.allowed(&Shown::default());
    let workloads = snapshot.workloads();
    let mut owners: Vec<&Object> = snapshot
        .ownership
        .workloads(namespace)
        .filter(|owner| model.serves(owner))
        .collect();
    owners.sort_by(|a, b| (&a.name, &a.kind).cmp(&(&b.name, &b.kind)));
    owners
        .into_iter()
        .map(|owner| {
            let named = |object: &Object| Named {
                text: transect_graph::links::kind_and_name_in(object, Some(namespace)),
                href: look.href(object),
            };
            let mut ports: Vec<String> = workloads
                .get(owner)
                .templates()
                .iter()
                .flat_map(|spec| spec.containers.iter())
                .flat_map(|container| container.ports.iter().flatten())
                .map(|port| port.container_port.to_string())
                .collect();
            ports.sort_by_key(|port| port.parse::<i32>().unwrap_or_default());
            ports.dedup();
            let serves = match ports.is_empty() {
                true => "serves a port a Service sends to".to_string(),
                false => format!("serves {}", ports.join(" · ")),
            };
            let reach = allowed.reach(owner);
            let on_machine = reach == Some(&Reach::MachineNetwork);
            let limited: Vec<Named> = match on_machine {
                true => Vec::new(),
                false => allowed
                    .limits_in(owner)
                    .into_iter()
                    .flatten()
                    .map(|name| {
                        let policy =
                            Object::new("networking.k8s.io", "NetworkPolicy", Some(namespace), name);
                        Named {
                            text: name.clone(),
                            href: look.href(&policy),
                        }
                    })
                    .collect(),
            };
            let unlimited = match on_machine {
                true => "none applies to a pod on its machine's network",
                false => "none",
            };
            let limits_out = !on_machine && allowed.limits_out(owner).is_some();
            let out = match (limits_out, on_machine) {
                (true, _) => gate(State::On, "A NetworkPolicy limits its calls out."),
                (false, true) => gate(
                    State::Off,
                    "No NetworkPolicy applies to a pod on its machine's network, so it may call any address.",
                ),
                (false, false) => gate(
                    State::Off,
                    "No NetworkPolicy limits its calls out, so it may call any address.",
                ),
            };
            let isolation = match (reach, on_machine) {
                (_, true) => Some(ways::Isolation::MachineNetwork),
                (Some(Reach::Isolated(_)), _) => Some(ways::Isolation::Isolated),
                _ => None,
            };
            let policy = ways::policy::of_workload(isolation, allowed.admissions(owner));
            let policy = gate(policy.state, &policy.title);
            let mine = ways.rows.iter().filter(|way| way.reaches.contains(owner));
            let mut mine: Vec<&ways::Way> = mine.collect();
            mine.sort_by(|a, b| a.order(b));
            Served {
                name: named(owner),
                serves,
                ways: mine.into_iter().map(|w| way(model, owner, w, look)).collect(),
                limited,
                unlimited,
                out,
                policy,
            }
        })
        .collect()
}

/// One way in, as a row of the page shows it.
fn way(model: &Model, owner: &Object, way: &ways::Way, look: &Look) -> Way {
    let href = |object: &Object| look.href(object);
    let how = match way.through {
        Through::HostPort => Some("host port"),
        Through::MachineNetwork => Some("on its machine's network"),
        Through::LoadBalancer => Some("LoadBalancer"),
        Through::ExternalIp => Some("external IP"),
        Through::NodePort => Some("node port"),
        Through::Host => None,
    };
    let namespace = owner.namespace.as_deref();
    let declared_at = match way.through {
        Through::HostPort | Through::MachineNetwork => {
            let intent = model.snapshot.intents.of(owner);
            let line = intent.and_then(|intent| intent.host_access.clone());
            line.map(|line| (line.text, line.provenance))
        }
        _ => None,
    };
    let (machine, name) = way.on_machine();
    Way {
        name,
        machine,
        declared_at,
        via: way
            .via
            .iter()
            .map(|object| Named {
                text: transect_graph::links::kind_and_name_in(object, namespace),
                href: look.href(object),
            })
            .collect(),
        how,
        ports: way.ports.clone(),
        gates: [
            Dot::of(&way.tls, &href),
            Dot::of(&way.sign_in, &href),
            Dot::of(&way.policy, &href),
        ],
    }
}

fn gate(state: State, title: &str) -> Dot {
    let gate = Gate {
        state,
        title: title.into(),
        names: Vec::new(),
        declared_at: None,
    };
    Dot::of(&gate, &|_| None)
}
