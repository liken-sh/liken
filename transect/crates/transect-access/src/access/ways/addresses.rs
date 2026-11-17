//! The ways in that answer without an Ingress: LoadBalancer and
//! NodePort Services, with a LoadBalancer's node ports a row of their
//! own, a Service's external IPs, host ports, and pods on their
//! machine's network.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::core::v1::{Pod, Service};
use kube::ResourceExt;

use super::backends::Target;
use super::{
    Backend, Gate, Leg, Sources, State, Through, Way, Ways, declared_sign_in, listeners, machines,
    machines_named, policy,
};
use transect_graph::edges::Object;

/// Reads the rows of each Service, host port, and pod on its machine's
/// network.  It also records the Service that each controller in
/// `controllers` answers on, which is the hosts' way in and not a row.
pub fn read(sources: &Sources, controllers: &BTreeSet<Object>, ways: &mut Ways) {
    let listening = listeners(sources);
    let node_addresses: BTreeSet<String> = sources
        .nodes
        .iter()
        .flat_map(|node| {
            node.status
                .iter()
                .flat_map(|s| s.addresses.iter().flatten())
        })
        .map(|address| address.address.clone())
        .collect();
    for service in sources.services {
        let spec = service.spec.as_ref();
        let kind = spec
            .and_then(|spec| spec.type_.as_deref())
            .unwrap_or("ClusterIP");
        let object = Object::new(
            "",
            "Service",
            service.namespace().as_deref(),
            &service.name_any(),
        );
        let reaches = (sources.selected)(&object);
        // Each address that the Service answers on, with its ports, and
        // the address of the Service's type first.  A LoadBalancer
        // Service also answers on its node ports, on every machine's
        // address, unless `allocateLoadBalancerNodePorts: false` left
        // them unset.  A Service of any type answers on its
        // `externalIPs`.
        let node_ports = ports(service, |p| p.node_port);
        let mut answers: Vec<(Through, Vec<String>, String)> = Vec::new();
        let mut machine = None;
        match kind {
            "LoadBalancer" => {
                let key = (service.namespace().unwrap_or_default(), service.name_any());
                let name = match listening.get(&key) {
                    Some(nodes) => {
                        let (name, one) = machines_named(nodes);
                        machine = one;
                        name
                    }
                    None => balanced(service, &node_addresses),
                };
                let own = ports(service, |p| Some(p.port));
                answers.push((Through::LoadBalancer, own, name));
                if !node_ports.is_empty() {
                    answers.push((Through::NodePort, node_ports, EVERY_MACHINE.into()));
                }
            }
            "NodePort" => answers.push((Through::NodePort, node_ports, EVERY_MACHINE.into())),
            _ => {}
        }
        let external = spec.and_then(|spec| spec.external_ips.clone());
        if let Some(external) = external.filter(|ips| !ips.is_empty()) {
            let own = ports(service, |p| Some(p.port));
            answers.push((Through::ExternalIp, own, external.join(", ")));
        }
        let controller = ways
            .entered
            .iter_mut()
            .find(|e| reaches.contains(&e.workload) && controllers.contains(&e.workload));
        // The first address that an ingress controller's Service answers
        // on is the hosts' way in.  The page names it in the line under
        // the table, so it gets no row.
        if let Some(entered) = controller
            && !answers.is_empty()
        {
            let (_, ports, name) = answers.remove(0);
            entered.service.get_or_insert((object.clone(), ports, name));
        }
        for (through, ports, name) in answers {
            let legs = legs(service, through);
            ways.rows.push(Way {
                through,
                name,
                via: vec![object.clone()],
                ports,
                tls: nothing_tls(),
                sign_in: declared_sign_in(&reaches, sources),
                policy: policy::gate(&reaches, &legs, None, sources),
                reaches: reaches.clone(),
                backends: super::backends::of_service(service),
                machine: machine.clone().filter(|_| through == Through::LoadBalancer),
            });
        }
    }
    type Held = (
        BTreeSet<String>,
        BTreeSet<String>,
        BTreeSet<Backend>,
        BTreeSet<(String, Target)>,
    );
    let mut held: BTreeMap<(Through, Object), Held> = BTreeMap::new();
    for pod in sources
        .pods
        .iter()
        .filter(|pod| !transect_graph::pods::finished(pod))
    {
        let Some(workload) = (sources.workload_of)(pod) else {
            continue;
        };
        let spec = pod.spec.as_ref();
        let node = spec
            .and_then(|spec| spec.node_name.clone())
            .unwrap_or_default();
        let on_machine = spec.and_then(|spec| spec.host_network).unwrap_or(false);
        let listener =
            listeners::service_of(pod, sources.daemon_sets, sources.load_balancers).is_some();
        let (through, ports) = match (on_machine, listener) {
            (true, _) => (Through::MachineNetwork, container_ports(pod, false)),
            (false, true) => continue,
            (false, false) => (Through::HostPort, container_ports(pod, true)),
        };
        if through == Through::HostPort && ports.is_empty() {
            continue;
        }
        let entry = held.entry((through, workload)).or_default();
        entry.0.extend(ports);
        entry.1.insert(node);
        entry
            .2
            .extend(super::backends::of_pod(pod, through == Through::HostPort));
        if through == Through::HostPort {
            entry.3.extend(host_legs(pod));
        }
    }
    for ((through, workload), (ports, nodes, backends, pairs)) in held {
        let mut ports: Vec<u32> = ports.iter().filter_map(|p| p.parse().ok()).collect();
        ports.sort();
        let ports: Vec<String> = match ports.is_empty() {
            true => vec!["any port it opens".into()],
            false => ports.iter().map(u32::to_string).collect(),
        };
        let reaches = vec![workload];
        let mut legs: Vec<Leg> = Vec::new();
        for (port, target) in pairs {
            match legs.iter_mut().find(|leg| leg.port == port) {
                Some(leg) => leg.targets.push(target),
                None => legs.push(Leg {
                    port,
                    targets: vec![target],
                }),
            }
        }
        let (name, machine) = machines_named(&nodes);
        ways.rows.push(Way {
            through,
            name,
            machine,
            via: Vec::new(),
            ports,
            tls: nothing_tls(),
            sign_in: declared_sign_in(&reaches, sources),
            policy: policy::gate(&reaches, &legs, None, sources),
            reaches,
            backends: backends.into_iter().collect(),
        });
    }
}

/// Where a node port answers.
const EVERY_MACHINE: &str = "every machine's address";

/// What a LoadBalancer Service answers on before it is given an
/// address.
pub const UNADDRESSED: &str = "no address yet";

fn nothing_tls() -> Gate {
    Gate::of(State::Off, "Nothing in the cluster puts TLS on this way.")
}

/// The machines each Service's listeners answer on, by the Service's
/// namespace and name.
fn listeners(sources: &Sources) -> BTreeMap<(String, String), BTreeSet<String>> {
    let mut found: BTreeMap<(String, String), BTreeSet<String>> = BTreeMap::new();
    for pod in sources
        .pods
        .iter()
        .filter(|pod| !transect_graph::pods::finished(pod))
    {
        let Some(service) = listeners::service_of(pod, sources.daemon_sets, sources.load_balancers)
        else {
            continue;
        };
        let node = pod.spec.as_ref().and_then(|spec| spec.node_name.clone());
        found.entry(service).or_default().extend(node);
    }
    found
}

/// A LoadBalancer Service's addresses in words: a count of machines
/// when every address it was given is a machine's address, and the
/// addresses themselves otherwise.
fn balanced(service: &Service, nodes: &BTreeSet<String>) -> String {
    let status = service
        .status
        .as_ref()
        .and_then(|s| s.load_balancer.as_ref());
    let given = status
        .and_then(|lb| lb.ingress.as_ref())
        .into_iter()
        .flatten();
    let addresses: Vec<String> = given
        .filter_map(|i| i.ip.clone().or_else(|| i.hostname.clone()))
        .collect();
    match addresses.as_slice() {
        [] => UNADDRESSED.into(),
        all if all.iter().all(|a| nodes.contains(a)) => machines(all.len()),
        all => all.join(", "),
    }
}

/// The ports `service` declares, as `port` reads each.
fn ports(
    service: &Service,
    port: impl Fn(&k8s_openapi::api::core::v1::ServicePort) -> Option<i32>,
) -> Vec<String> {
    let spec = service.spec.as_ref();
    let declared = spec
        .and_then(|spec| spec.ports.as_ref())
        .into_iter()
        .flatten();
    let mut found: Vec<i32> = declared.filter_map(port).collect();
    found.sort();
    found.dedup();
    found.iter().map(i32::to_string).collect()
}

/// Each port of `service` that a way of `through` answers on, with the
/// port of the pods behind it.  A node port's way answers on the node
/// port, and any other way answers on the Service's own port.
fn legs(service: &Service, through: Through) -> Vec<Leg> {
    let spec = service.spec.as_ref();
    let declared = spec
        .and_then(|spec| spec.ports.as_ref())
        .into_iter()
        .flatten();
    let mut legs: Vec<Leg> = Vec::new();
    for port in declared {
        let number = match through {
            Through::NodePort => port.node_port,
            _ => Some(port.port),
        };
        let Some(number) = number else {
            continue;
        };
        legs.push(Leg {
            port: number.to_string(),
            targets: vec![super::backends::target(port)],
        });
    }
    legs.sort();
    legs.dedup();
    legs
}

/// Each host port of `pod` that answers outside its machine, with the
/// container port it forwards to.
fn host_legs(pod: &Pod) -> Vec<(String, Target)> {
    let spec = pod.spec.iter();
    let containers = spec.flat_map(|spec| spec.containers.iter());
    let ports = containers.flat_map(|c| c.ports.iter().flatten());
    ports
        .filter_map(|p| {
            let host = super::backends::host_port(p)?;
            Some((host.to_string(), Target::Number(p.container_port)))
        })
        .collect()
}

/// The ports `pod`'s containers declare: their host ports when `host`
/// is true, and their container ports otherwise.
fn container_ports(pod: &Pod, host: bool) -> Vec<String> {
    let spec = pod.spec.iter();
    let containers = spec.flat_map(|spec| spec.containers.iter());
    let ports = containers.flat_map(|c| c.ports.iter().flatten());
    ports
        .filter_map(|p| match host {
            true => super::backends::host_port(p),
            false => Some(p.container_port),
        })
        .map(|p| p.to_string())
        .collect()
}
