//! What an address reaches on the map.  Each end from the index stands
//! for the workloads a `Service` selects.  An address that names an
//! `Ingress` host on a port the `Ingress` does not answer on is
//! followed to the other `Service` of the same workload.

use super::index::Addresses;
use super::parse::Address;
use transect_graph::edges::{Endpoint, Graph, Kind};

/// The ends one address reaches.
#[derive(Debug, Default, PartialEq)]
pub struct Reach {
    /// Each end once, in the order the index gives them.
    pub to: Vec<Endpoint>,
    /// Whether the ends are a guess: the address names an `Ingress`
    /// host on a port the `Ingress` does not answer on, and no `Service`
    /// of the `Ingress`'s workload exposes that port either, so the
    /// workload is only the likeliest server of it.
    pub guessed: bool,
}

/// What `address` reaches when it is configured in `namespace`.  A
/// forge's URLs show why the `Ingress` host rule exists: its web host
/// is an `Ingress`, while its SSH port on the same host is served by a
/// `NodePort` or `LoadBalancer` `Service` of the same workload, and a
/// GitOps source often fetches over SSH.  Without the rule that source
/// reaches nothing.
pub fn reach(
    addresses: &Addresses,
    graph: &Graph,
    address: &Address,
    namespace: Option<&str>,
) -> Reach {
    let mut reach = Reach::default();
    for end in addresses.resolve(address, namespace) {
        reach.add(behind(graph, end));
    }
    let Some(port) = address.port.filter(|_| reach.to.is_empty()) else {
        return reach;
    };
    let workloads: Vec<Endpoint> = addresses
        .off_ingress_port(address)
        .into_iter()
        .flat_map(|backend| behind(graph, backend))
        .collect();
    for workload in &workloads {
        let services = graph.reaching(Kind::Selects, workload);
        for service in services.into_iter().filter(|s| addresses.exposes(s, port)) {
            reach.add(behind(graph, service));
        }
    }
    if reach.to.is_empty() {
        reach.add(workloads);
        reach.guessed = !reach.to.is_empty();
    }
    reach
}

impl Reach {
    fn add(&mut self, ends: Vec<Endpoint>) {
        for end in ends {
            if !self.to.contains(&end) {
                self.to.push(end);
            }
        }
    }
}

/// What an end of the index stands for on the map: a `Service` stands
/// for the workloads it selects.  A `Service` that selects nothing,
/// such as one whose endpoints are named by hand, stands for itself, as
/// a machine does.
pub fn behind(graph: &Graph, end: &Endpoint) -> Vec<Endpoint> {
    let selected = graph.leaving(Kind::Selects, end);
    if selected.is_empty() {
        vec![end.clone()]
    } else {
        selected.into_iter().cloned().collect()
    }
}
