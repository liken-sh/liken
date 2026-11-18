//! Evaluates each `networking.k8s.io/v1` `NetworkPolicy` to find which
//! pods may call which, and reports the result for each top owner and
//! each namespace.
//!
//! A pod may reach another on a port when the target's ingress rules
//! and the caller's egress rules both admit it. A pod that no policy
//! selects for a direction is open in that direction. So the module
//! lists callers only for a target that some policy isolates for
//! ingress. For every other target it reports that any pod may call
//! it, instead of listing every workload in the cluster. The module
//! does not read a CNI's own policy kinds, so on a cluster that uses
//! them this view allows more than the network does.
//!
//! The module reads only the API, so it matches an address block
//! against the addresses in a pod's status. A pod that uses its
//! machine's network has its machine's address there, and its calls
//! come from that address. So a pod selector or namespace selector
//! never admits it as a caller, and a block that contains the address
//! does. No `NetworkPolicy` applies to such a pod in either direction,
//! so nothing limits who calls it or what it calls.

mod admission;
mod card;
mod cidr;
#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures;
mod members;
mod notes;
mod ports;
mod rules;
mod sentence;
#[cfg(test)]
mod tests;

use std::collections::{BTreeMap, BTreeSet, HashMap};

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::networking::v1::NetworkPolicy;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

pub use admission::{Admission, Caller};
pub use card::Calls;
pub use members::Idle;
pub use ports::Ports;

use admission::{addresses, admission};
use members::Member;
use rules::Compiled;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};

/// Whether policy isolates a workload's pods for ingress, and who may
/// call them when it does.
#[derive(Clone, Debug, PartialEq)]
pub enum Reach {
    /// At least one of its pods has no ingress policy that selects it,
    /// so any pod may call the workload.
    Open,
    /// Its pods are on their machine's network, where no
    /// `NetworkPolicy` applies, so any pod may call it at its machine's
    /// address.
    MachineNetwork,
    /// Policy isolates every one of its pods.  The map lists each
    /// caller that may call it, with the ports that the policies of
    /// both ends admit.  The map omits the workload's own pods.
    Isolated(BTreeMap<Caller, Ports>),
}

/// Whether policy isolates a namespace's pods, and which other
/// namespaces may call them.
#[derive(Clone, Debug, PartialEq)]
pub enum NamespaceReach {
    /// No policy isolates any of its pods.
    Open,
    /// Policy isolates some of the namespace's workloads but not all.
    /// `open` lists each workload with a pod that no ingress policy
    /// selects, and `machine_network` each workload with a pod on its
    /// machine's network.
    Partly {
        open: Vec<Object>,
        machine_network: Vec<Object>,
    },
    /// Every workload is isolated, and pods of these other namespaces
    /// may call one or more of them.
    Isolated(BTreeSet<String>),
}

/// The result of evaluating the policies of a cluster.
#[derive(Debug, Default)]
pub struct Allowed {
    /// The `Reach` of each workload with a pod or a template, by top
    /// owner.
    reach: HashMap<Object, Reach>,
    /// The workloads with a pod or a template in each namespace, sorted.
    workloads: BTreeMap<String, BTreeSet<Object>>,
    /// The other namespaces whose pods may call each namespace's
    /// isolated workloads.
    callers: HashMap<String, BTreeSet<String>>,
    /// For each isolated workload, the namespaces whose every pod some
    /// rule admits, with no pod selector or an empty one. A card says
    /// "every pod in" a namespace only for these, because a selector
    /// that matches every current pod of a namespace may not match pods
    /// added later.
    whole: HashMap<Object, BTreeSet<String>>,
    /// For each isolated workload, the ports on which some rule admits
    /// every pod of every namespace. A workload with no such port has no
    /// entry.
    everywhere: HashMap<Object, Ports>,
    /// The rules that admit callers to each isolated workload.
    admissions: HashMap<Object, Vec<Admission>>,
    /// The policies that limit each workload's calls out, by name.
    limits_out: HashMap<Object, BTreeSet<String>>,
    /// The policies that limit each workload's calls in, by name.
    limits_in: HashMap<Object, BTreeSet<String>>,
}

impl Allowed {
    /// Evaluates `policies` over `pods`, and over the templates of each
    /// of `idle` that has no running pod.  `namespaces` gives the labels
    /// a namespace selector reads, and `ownership` each pod's top owner.
    pub fn evaluate<'a>(
        policies: impl IntoIterator<Item = &'a NetworkPolicy>,
        pods: impl IntoIterator<Item = &'a Pod>,
        idle: impl IntoIterator<Item = Idle<'a>>,
        namespaces: impl IntoIterator<Item = &'a ObjectMeta>,
        ownership: &Ownership,
    ) -> Self {
        let policies: Vec<&NetworkPolicy> = policies.into_iter().collect();
        let keys = members::read_keys(&policies);
        let members = members::members(pods, idle, &keys, ownership);
        let compiled = rules::compile(policies, &members, namespaces);
        let mut allowed = Self::default();
        for member in &members {
            let owners = allowed
                .workloads
                .entry(member.namespace.clone())
                .or_default();
            owners.insert(member.owner.clone());
        }
        // Members are sorted by owner, so the members of one owner are
        // adjacent. `first[i]` is the index of the first member with
        // member `i`'s owner. The loops below key each owner by that
        // index, so the callers of all of an owner's pods merge into
        // one entry for each calling owner.
        let first: Vec<usize> = (0..members.len())
            .scan(0, |first, index| {
                if members[index].owner != members[*first].owner {
                    *first = index;
                }
                Some(*first)
            })
            .collect();
        for (index, names) in compiled.limits_out.iter().enumerate() {
            if !names.is_empty() {
                let limits = allowed.limits_out.entry(members[index].owner.clone());
                limits
                    .or_default()
                    .extend(names.iter().map(|n| n.to_string()));
            }
        }
        for (index, names) in compiled.limits_in.iter().enumerate() {
            if !names.is_empty() {
                let limits = allowed.limits_in.entry(members[index].owner.clone());
                limits
                    .or_default()
                    .extend(names.iter().map(|n| n.to_string()));
            }
        }
        let mut open = vec![false; members.len()];
        for index in (0..members.len()).filter(|&index| compiled.ingress[index].is_none()) {
            open[first[index]] = true;
        }
        let mut gathered: BTreeMap<usize, Gathered> = BTreeMap::new();
        for target in 0..members.len() {
            if open[first[target]] {
                continue;
            }
            let found = callers(&compiled, &members, target);
            let namespace = &members[target].namespace;
            let callers = allowed.callers.entry(namespace.clone()).or_default();
            for (caller, _) in &found.pods {
                if &members[*caller].namespace != namespace {
                    callers.insert(members[*caller].namespace.clone());
                }
            }
            let into = gathered.entry(first[target]).or_default();
            into.admissions.extend(found.admissions);
            into.whole.extend(found.whole);
            unite(&mut into.everywhere, &found.everywhere);
            for (caller, ports) in found.pods {
                unite(
                    into.pods.entry(first[caller]).or_insert_with(Ports::none),
                    &ports,
                );
            }
            for (caller, ports) in found.outside {
                unite(
                    into.outside.entry(caller).or_insert_with(Ports::none),
                    &ports,
                );
            }
        }
        for (index, member) in members.iter().enumerate() {
            if !open[index] {
                continue;
            }
            let reach = if members[first[index]..]
                .iter()
                .take_while(|other| other.owner == member.owner)
                .any(|other| other.host_network)
            {
                Reach::MachineNetwork
            } else {
                Reach::Open
            };
            allowed.reach.insert(member.owner.clone(), reach);
        }
        for (target, found) in gathered {
            let named = found.pods.into_iter().map(|(caller, ports)| {
                let member = &members[caller];
                let caller = Caller::Workload {
                    namespace: member.namespace.clone(),
                    owner: member.owner.clone(),
                };
                (caller, ports)
            });
            let all = named.chain(found.outside).collect();
            let owner = members[target].owner.clone();
            allowed
                .admissions
                .insert(owner.clone(), found.admissions.into_iter().collect());
            allowed.whole.insert(owner.clone(), found.whole);
            if !found.everywhere.is_empty() {
                allowed.everywhere.insert(owner.clone(), found.everywhere);
            }
            allowed.reach.insert(owner, Reach::Isolated(all));
        }
        allowed
    }

    /// Whether policy isolates `workload`, and who may call it.  Returns
    /// `None` for a workload with neither a running pod nor a template,
    /// such as a custom resource whose operator has not started its
    /// pods.
    pub fn reach(&self, workload: &Object) -> Option<&Reach> {
        self.reach.get(workload)
    }

    /// The rules that admit callers to `workload`, when policy isolates
    /// it.
    pub fn admissions(&self, workload: &Object) -> &[Admission] {
        self.admissions.get(workload).map_or(&[], Vec::as_slice)
    }

    /// The policies that limit `workload`'s calls out, by name.
    pub fn limits_out(&self, workload: &Object) -> Option<&BTreeSet<String>> {
        self.limits_out.get(workload)
    }

    /// The policies that limit `workload`'s calls in, by name.
    pub fn limits_in(&self, workload: &Object) -> Option<&BTreeSet<String>> {
        self.limits_in.get(workload)
    }

    /// Whether policy isolates `namespace`'s workloads, or `None` when it
    /// has none with a pod or a template.
    pub fn of_namespace(&self, namespace: &str) -> Option<NamespaceReach> {
        let workloads = self.workloads.get(namespace)?;
        let reached = |reach: Reach| {
            let workloads = workloads.iter();
            let found = workloads.filter(|workload| self.reach.get(*workload) == Some(&reach));
            found.cloned().collect::<Vec<Object>>()
        };
        let (open, machine_network) = (reached(Reach::Open), reached(Reach::MachineNetwork));
        Some(match open.len() + machine_network.len() {
            0 => NamespaceReach::Isolated(self.callers.get(namespace).cloned().unwrap_or_default()),
            count if count == workloads.len() => NamespaceReach::Open,
            _ => NamespaceReach::Partly {
                open,
                machine_network,
            },
        })
    }

    /// Whether a rule admits every pod of `namespace` to `workload`.
    pub fn admits_whole(&self, workload: &Object, namespace: &str) -> bool {
        self.whole
            .get(workload)
            .is_some_and(|whole| whole.contains(namespace))
    }

    /// Whether a rule admits every namespace to `workload` on each of
    /// `ports`. Returns false when `ports` is empty.
    pub fn admits_every_namespace(&self, workload: &Object, ports: &Ports) -> bool {
        let Some(everywhere) = self.everywhere.get(workload) else {
            return false;
        };
        !ports.is_empty() && &ports.intersect(everywhere) == ports
    }

    /// Whether a rule admits every namespace to any of `namespace`'s
    /// workloads.
    pub fn admits_every_namespace_in(&self, namespace: &str) -> bool {
        let mut workloads = self.workloads(namespace);
        workloads.any(|workload| self.everywhere.contains_key(workload))
    }

    /// The workloads with a pod or a template in `namespace`.
    pub fn workloads(&self, namespace: &str) -> impl Iterator<Item = &Object> {
        self.workloads.get(namespace).into_iter().flatten()
    }

    /// The namespaces with a workload that has a pod or a template.
    pub fn namespaces(&self) -> impl Iterator<Item = &str> {
        self.workloads.keys().map(String::as_str)
    }

    /// An allowed-to-call edge from each workload to each isolated
    /// workload that it may call. A workload that is not isolated gets no
    /// edges, because any pod may call it.
    pub fn edges(&self) -> Vec<Edge> {
        let mut edges = Vec::new();
        for (target, reach) in &self.reach {
            let Reach::Isolated(callers) = reach else {
                continue;
            };
            for caller in callers.keys() {
                if let Caller::Workload { owner, .. } = caller {
                    edges.push(Edge {
                        kind: Kind::AllowedToCall,
                        from: Endpoint::Object(owner.clone()),
                        to: Endpoint::Object(target.clone()),
                        guessed: false,
                        provenance: Provenance::Kubernetes,
                    });
                }
            }
        }
        edges
    }
}

/// The callers of one target workload, merged across its members.
/// `pods` keys each calling workload by the index of its first member.
/// `outside` holds the address blocks and `Caller::Anything`, which are
/// not members.
#[derive(Default)]
struct Gathered {
    admissions: BTreeSet<Admission>,
    pods: BTreeMap<usize, Ports>,
    outside: BTreeMap<Caller, Ports>,
    whole: BTreeSet<String>,
    everywhere: Ports,
}

/// The callers of one member, by index into the members.
struct Found {
    admissions: Vec<Admission>,
    pods: Vec<(usize, Ports)>,
    outside: Vec<(Caller, Ports)>,
    /// The namespaces whose every pod a rule admits.
    whole: BTreeSet<String>,
    /// The ports on which a rule admits every pod of every namespace.
    everywhere: Ports,
}

fn unite(known: &mut Ports, ports: &Ports) {
    *known = known.union(ports);
}

/// Who may call the member `target`, on which ports, by the target's
/// ingress rules and each caller's egress rules.  Other members of the
/// target's own workload are left out.
fn callers(compiled: &Compiled, members: &[Member], target: usize) -> Found {
    let mut outside = Vec::new();
    let mut whole = BTreeSet::new();
    let mut everywhere = Ports::none();
    let named = &members[target].named;
    // A map keyed by member index, because most rules admit few
    // members.
    let mut admitted: BTreeMap<usize, Ports> = BTreeMap::new();
    let mut admissions = Vec::new();
    let owner = &members[target].owner;
    for &index in compiled.ingress[target].iter().flatten() {
        let rule = &compiled.rules[index];
        let ports = Ports::of_rule(rule.ports, named);
        if ports.is_empty() {
            continue;
        }
        admissions.push(admission(rule, members, owner, ports.clone()));
        match &rule.peers {
            None => {
                outside.push((Caller::Anything, ports.clone()));
                for caller in 0..members.len() {
                    admit(&mut admitted, caller, &ports);
                }
            }
            Some(peers) => {
                for &caller in peers {
                    admit(&mut admitted, caller, &ports);
                }
            }
        }
        for block in &rule.blocks {
            outside.push((addresses(block), ports.clone()));
        }
        whole.extend(rule.whole.iter().cloned());
        if rule.everywhere {
            everywhere = everywhere.union(&ports);
        }
    }
    let pods = admitted
        .into_iter()
        .filter(|(caller, _)| &members[*caller].owner != owner)
        .map(|(caller, ports)| {
            let ports = ports.intersect(&egress(compiled, members, caller, target));
            (caller, ports)
        })
        .filter(|(_, ports)| !ports.is_empty())
        .collect();
    Found {
        admissions,
        pods,
        outside,
        whole,
        everywhere,
    }
}

fn admit(admitted: &mut BTreeMap<usize, Ports>, caller: usize, ports: &Ports) {
    admitted
        .entry(caller)
        .and_modify(|known| *known = known.union(ports))
        .or_insert_with(|| ports.clone());
}

/// The ports that `caller`'s egress rules admit to `target`, which is
/// every port when no egress policy selects `caller`.  A named port
/// resolves against the target's container ports, because the target
/// is the pod that the connection reaches.
fn egress(compiled: &Compiled, members: &[Member], caller: usize, target: usize) -> Ports {
    let Some(rules) = &compiled.egress[caller] else {
        return Ports::Any;
    };
    let named = &members[target].named;
    let rules = rules.iter().map(|&index| &compiled.rules[index]);
    rules
        .filter(|rule| rule.admits(target))
        .fold(Ports::none(), |ports, rule| {
            ports.union(&Ports::of_rule(rule.ports, named))
        })
}
