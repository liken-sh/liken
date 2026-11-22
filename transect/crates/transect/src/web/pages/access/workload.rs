//! A workload's access tab. It shows each way in to the workload's
//! ports (`ways::Way`: through an `Ingress`, a `Service`, a host port,
//! or the machine's network), whether a `NetworkPolicy` allows it, and
//! what the workload runs as, what it holds, and what it calls.

use k8s_openapi::jiff::Timestamp;

use super::super::{WorkloadAt, read_workloads, workload_card};
use super::calls::{calls, inside, isolation, out};
use super::change::change;
use super::dots::{gate, plain};
use super::holds::{holds, runs_as};
use super::look::Look;
use super::served::Served;
use crate::web::model::Model;
use transect_access::access::drawing::page::AccessPage;
use transect_access::access::drawing::reach::{self, Path, Reach, Right, ServiceBox, WayBox};
use transect_access::access::drawing::{self as drawn, Gate};
use transect_access::access::ways::{self, Backend, Through};
use transect_graph::edges::{Endpoint, Kind, Object};
use transect_map::map::WorkloadContents;
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};
use transect_observe::usage;
use transect_resources::layers::Layers;

/// The access tab of the workload `at`, or `None` when there is no such
/// workload or it serves no port.
pub fn access_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    now: Timestamp,
) -> Option<AccessPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let page = read_workloads(
        model,
        layers,
        usage,
        namespace,
        |map, contents| {
            let layer = map.layer_of(namespace)?.to_owned();
            let object =
                transect_map::map::find(&contents.owners, contents.plurals, resource, name)?;
            if !model.serves(object) {
                return None;
            }
            let workload_path = transect_graph::links::page(object, contents.plurals)?;
            let look = Look::new(model);
            let reach = reach::draw(scene(model, object, &contents, &look));
            let change = drawn::change::draw(change(model, object, &contents, &look));
            Some(AccessPage {
                namespace: namespace.into(),
                layer,
                name: name.into(),
                workload_path,
                reach,
                change,
                card: workload_card(object, &contents).with_access(None),
            })
        },
        now,
    )?;
    Some(page)
}

fn scene(model: &Model, object: &Object, contents: &WorkloadContents, look: &Look) -> Reach {
    let namespace = object.namespace.as_deref();
    let mut ports = Served::read(contents, object);
    let ways = model.ways();
    let mine: Vec<&ways::Way> = ways
        .rows
        .iter()
        .filter(|way| way.reaches.contains(object))
        .collect();
    let selected = |service: &Object| {
        let from = Endpoint::Object(service.clone());
        let selects = contents.graph.leaving(Kind::Selects, &from);
        selects
            .into_iter()
            .any(|end| end == &Endpoint::Object(object.clone()))
    };
    let mut services: Vec<(Object, ServiceBox)> = Vec::new();
    let mut controller = None;
    let mut way_boxes = Vec::new();
    for way in &mine {
        let policy = gate(&way.policy);
        let via: Vec<String> = way
            .via
            .iter()
            .map(|o| transect_graph::links::kind_and_name_in(o, namespace))
            .collect();
        let port = way.ports.join(", ");
        let (line, path) = match way.through {
            Through::Host => {
                if controller.is_none() {
                    controller = controller_of(model, way, look);
                }
                for backend in way
                    .backends
                    .iter()
                    .filter(|b| b.service.as_ref().is_some_and(selected))
                {
                    let at = service_box(&mut services, backend, true, &policy, contents, look);
                    let port = ports.of(&backend.port);
                    if !services[at].1.ports.contains(&port) {
                        services[at].1.ports.push(port);
                    }
                }
                (
                    format!("port {port} · {}", via.join(", ")),
                    Path::Controller,
                )
            }
            Through::LoadBalancer | Through::ExternalIp | Through::NodePort => {
                let mut path = Path::Ports {
                    ports: Vec::new(),
                    policy: policy.clone(),
                    label: String::new(),
                };
                for backend in &way.backends {
                    let at = service_box(&mut services, backend, false, &policy, contents, look);
                    let port = ports.of(&backend.port);
                    if !services[at].1.ports.contains(&port) {
                        services[at].1.ports.push(port);
                    }
                    path = Path::Service(at);
                }
                let kind = match way.through {
                    Through::LoadBalancer => "LoadBalancer",
                    Through::ExternalIp => "external IP",
                    _ => "node port",
                };
                (format!("port {port} · {kind}"), path)
            }
            Through::HostPort | Through::MachineNetwork => {
                let reached = way.backends.iter().map(|b| ports.of(&b.port)).collect();
                let (line, label) = match way.through {
                    Through::HostPort => (format!("host port {port}"), "through its host port"),
                    _ => (format!("port {port}"), "around the ingress controller"),
                };
                let path = Path::Ports {
                    ports: reached,
                    policy,
                    label: label.into(),
                };
                (line, path)
            }
        };
        let href = way.via.first().and_then(|o| look.href(o));
        let (title, line, href) = match way.through {
            Through::HostPort | Through::MachineNetwork => {
                machine_address(contents, object, way, line)
            }
            _ => (way.name.clone(), line, href),
        };
        way_boxes.push(WayBox {
            boxed: plain(title, vec![line], href),
            tls: gate(&way.tls),
            sign_in: gate(&way.sign_in),
            path,
        });
    }
    // A `Service` that selects the workload gets a box even when no way
    // in goes through it.
    let end = Endpoint::Object(object.clone());
    let isolation = isolation(model, object);
    for selecting in contents.graph.reaching(Kind::Selects, &end) {
        let Endpoint::Object(service) = selecting else {
            continue;
        };
        if services.iter().any(|(o, _)| o == service) {
            continue;
        }
        let found = contents.services.iter().find(|s| {
            s.metadata.namespace == service.namespace
                && s.metadata.name.as_deref() == Some(service.name.as_str())
        });
        for backend in found
            .map(|s| ways::backends::of_service(s))
            .unwrap_or_default()
        {
            let at = service_box(&mut services, &backend, false, &isolation, contents, look);
            let port = ports.of(&backend.port);
            if !services[at].1.ports.contains(&port) {
                services[at].1.ports.push(port);
            }
        }
    }
    let inside = inside(model, object, &mut ports, look);
    let workload = workload_box(object, contents);
    let right = Right {
        runs_as: runs_as(model, object, contents, look),
        holds: holds(model, object, contents, look),
        calls: calls(model, object, look),
        out: out(model, object),
    };
    Reach {
        ways: way_boxes,
        controller,
        services: services.into_iter().map(|(_, s)| s).collect(),
        inside,
        ports: ports.into_ports(),
        workload,
        right,
    }
}

/// The title, line, and link of a host port or machine network way in.
/// When the workload's running pods are all on one machine, the box is
/// titled with that machine and shows the pod's `hostIP`, or its first
/// pod IP when it has no `hostIP`.
fn machine_address(
    contents: &WorkloadContents,
    object: &Object,
    way: &ways::Way,
    line: String,
) -> (String, String, Option<String>) {
    let running = contents.workloads.get(object).running();
    let mut nodes: Vec<(String, String)> = running
        .iter()
        .filter_map(|pod| {
            let node = pod.spec.as_ref()?.node_name.clone()?;
            let status = pod.status.as_ref();
            let listed = status.and_then(|s| s.pod_ips.as_ref()?.first().map(|ip| ip.ip.clone()));
            let address = status.and_then(|s| s.host_ip.clone()).or(listed);
            Some((node, address.unwrap_or_default()))
        })
        .collect();
    nodes.sort();
    nodes.dedup();
    match nodes.as_slice() {
        [(node, address)] => {
            let line = match address.is_empty() {
                true => line,
                false => format!("{line} · {address}"),
            };
            let href = Some(transect_graph::links::machine(node));
            (format!("{node}'s address"), line, href)
        }
        _ => (way.name.clone(), line, None),
    }
}

/// The ingress controller box of a host way.
fn controller_of(model: &Model, way: &ways::Way, look: &Look) -> Option<SectionBox> {
    let snapshot = &model.snapshot;
    let ingress = way.via.first()?;
    let found = snapshot.ingresses.iter().find(|i| {
        i.metadata.namespace == ingress.namespace
            && i.metadata.name.as_deref() == Some(ingress.name.as_str())
    })?;
    let serving = snapshot.serving();
    let workload = snapshot.ingress_controller(&serving, found).workload?;
    let lines = vec![
        transect_graph::links::kind_and_name_in(&workload, None),
        "the ingress controller".into(),
    ];
    Some(plain(workload.name.clone(), lines, look.href(&workload)))
}

/// The index of `backend`'s `Service` in `services`, after adding its
/// box when it has none. A backend with no `Service` returns 0, so
/// callers pass only `Service` backends.
fn service_box(
    services: &mut Vec<(Object, ServiceBox)>,
    backend: &Backend,
    from_controller: bool,
    policy: &Gate,
    contents: &WorkloadContents,
    look: &Look,
) -> usize {
    let Some(service) = &backend.service else {
        return 0;
    };
    if let Some(at) = services.iter().position(|(o, _)| o == service) {
        services[at].1.from_controller |= from_controller;
        return at;
    }
    let found = contents.services.iter().find(|s| {
        s.metadata.namespace == service.namespace
            && s.metadata.name.as_deref() == Some(service.name.as_str())
    });
    let kind = found
        .and_then(|s| s.spec.as_ref()?.type_.clone())
        .unwrap_or_else(|| "ClusterIP".into());
    let numbers: Vec<String> = found
        .iter()
        .flat_map(|s| s.spec.iter().flat_map(|spec| spec.ports.iter().flatten()))
        .map(|p| p.port.to_string())
        .collect();
    let line = format!("{kind} · port {}", numbers.join(", "));
    let boxed = plain(
        format!("Service {}", service.name),
        vec![line],
        look.href(service),
    );
    services.push((
        service.clone(),
        ServiceBox {
            boxed,
            ports: Vec::new(),
            policy: policy.clone(),
            from_controller,
        },
    ));
    services.len() - 1
}

/// The workload's box, with its running pods and their machines. The
/// box has no link, because the page is the workload's own. When the
/// pods run on one machine, the machine's name links to its page.
fn workload_box(object: &Object, contents: &WorkloadContents) -> SectionBox {
    let workload = contents.workloads.get(object);
    let running = workload.running();
    let mut nodes: Vec<String> = running
        .iter()
        .filter_map(|pod| pod.spec.as_ref()?.node_name.clone())
        .collect();
    nodes.sort();
    nodes.dedup();
    let title = transect_graph::edges::kind_and_name(&object.kind, &object.name);
    let on = |count: usize, node: &str| {
        let pods = match count {
            1 => "1 pod on".to_string(),
            count => format!("{count} pods on"),
        };
        let machine = transect_graph::links::Part {
            text: node.to_string(),
            href: Some(transect_graph::links::machine(node)),
        };
        SectionBox::new(title.clone(), vec![pods, String::new()], BoxLook::Workload)
            .with_names(vec![machine])
    };
    match (running.len(), nodes.as_slice()) {
        (0, _) => SectionBox::new(title, vec!["no pod running".into()], BoxLook::Workload),
        (count, [node]) => on(count, node),
        (count, nodes) => SectionBox::new(
            title,
            vec![format!("{count} pods on {} machines", nodes.len())],
            BoxLook::Workload,
        ),
    }
}
