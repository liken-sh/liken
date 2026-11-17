//! Each `NetworkPolicy` compiled against the members: for each rule,
//! the members its peers match, and for each member, the rules that
//! apply to it in each direction.
//!
//! A rule's peers are matched once, when it is compiled, and a policy
//! that selects ten pods shares its rules among them, so the work grows
//! with the number of rules times members, not with pairs of pods.

use std::collections::{BTreeMap, HashMap, HashSet};

use k8s_openapi::api::networking::v1::{
    IPBlock, NetworkPolicy, NetworkPolicyPeer, NetworkPolicyPort,
};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{LabelSelector, ObjectMeta};

use super::cidr::Block;
use super::members::Member;
use transect_graph::selector;

/// One rule of one policy, in either direction.
#[derive(Debug)]
pub struct Rule<'a> {
    /// The name of the policy the rule is in.
    pub policy: &'a str,
    /// The members the rule's peers match, sorted, or `None` for a rule
    /// that lists no peers, which admits every source or destination.
    pub peers: Option<Vec<usize>>,
    /// The rule's address blocks, which an ingress rule admits from
    /// outside the cluster too.
    pub blocks: Vec<&'a IPBlock>,
    /// The namespaces a peer admits whole, with no pod selector or an
    /// empty one, so a card can show "every pod in" only when the rule
    /// admits the whole namespace, not when a selector matches every
    /// pod today.
    pub whole: Vec<String>,
    /// Whether the rule admits every namespace whole: it lists no peers,
    /// or a peer with an empty namespace selector and no pod selector
    /// or an empty one.  Only then may a card show "any namespace",
    /// because a label that every namespace has today admits no
    /// namespace that is added later without the label.
    pub everywhere: bool,
    pub ports: &'a [NetworkPolicyPort],
}

impl Rule<'_> {
    pub fn admits(&self, member: usize) -> bool {
        self.peers
            .as_ref()
            .is_none_or(|peers| peers.binary_search(&member).is_ok())
    }
}

/// The rules that apply to each member in one direction: `None` for a
/// member no policy of that type selects, which is open in that
/// direction, and the indexes of the rules otherwise.  A member that
/// policy selects with an empty rule list has an empty list: it admits
/// nothing.
pub type Applied = Vec<Option<Vec<usize>>>;

#[derive(Debug, Default)]
pub struct Compiled<'a> {
    pub rules: Vec<Rule<'a>>,
    pub ingress: Applied,
    pub egress: Applied,
    /// The names of the policies that limit each member's calls in,
    /// which a policy with no ingress rules does too.
    pub limits_in: Vec<Vec<&'a str>>,
    /// The names of the policies that limit each member's calls out,
    /// which a policy with no egress rules does too.
    pub limits_out: Vec<Vec<&'a str>>,
}

/// Compiles `policies` against `members`.  `namespaces` gives each
/// namespace's labels, which a namespace selector reads.
pub fn compile<'a>(
    policies: impl IntoIterator<Item = &'a NetworkPolicy>,
    members: &[Member],
    namespaces: impl IntoIterator<Item = &'a ObjectMeta>,
) -> Compiled<'a> {
    let none = BTreeMap::new();
    let labels: BTreeMap<&str, &BTreeMap<String, String>> = namespaces
        .into_iter()
        .filter_map(|meta| Some((meta.name.as_deref()?, meta.labels.as_ref().unwrap_or(&none))))
        .collect();
    let mut by_namespace: HashMap<&str, Vec<usize>> = HashMap::new();
    for (index, member) in members.iter().enumerate() {
        by_namespace
            .entry(&member.namespace)
            .or_default()
            .push(index);
    }
    let mut compiled = Compiled {
        rules: Vec::new(),
        ingress: vec![None; members.len()],
        egress: vec![None; members.len()],
        limits_in: vec![Vec::new(); members.len()],
        limits_out: vec![Vec::new(); members.len()],
    };
    for policy in policies {
        let (Some(namespace), Some(spec)) = (policy.metadata.namespace.as_deref(), &policy.spec)
        else {
            continue;
        };
        let name = policy.metadata.name.as_deref().unwrap_or_default();
        let here = by_namespace.get(namespace).into_iter().flatten().copied();
        // No policy selects a pod on its machine's network.
        let selected: Vec<usize> = here
            .filter(|&index| !members[index].host_network)
            .filter(|&index| {
                spec.pod_selector
                    .as_ref()
                    .is_none_or(|selector| selector::matches(selector, &members[index].labels))
            })
            .collect();
        if selected.is_empty() {
            continue;
        }
        let (ingress, egress) = types(policy);
        let scope = Scope {
            namespace,
            members,
            labels: &labels,
            by_namespace: &by_namespace,
        };
        if ingress {
            let rules = spec.ingress.iter().flatten();
            let peers = rules.map(|rule| (rule.from.as_deref(), rule.ports.as_deref()));
            let added = compiled.add(name, peers, &scope);
            apply(&mut compiled.ingress, &selected, &added);
            for &member in &selected {
                compiled.limits_in[member].push(name);
            }
        }
        if egress {
            let rules = spec.egress.iter().flatten();
            let peers = rules.map(|rule| (rule.to.as_deref(), rule.ports.as_deref()));
            let added = compiled.add(name, peers, &scope);
            apply(&mut compiled.egress, &selected, &added);
            for &member in &selected {
                compiled.limits_out[member].push(name);
            }
        }
    }
    compiled
}

/// Which directions a policy isolates.  The API server fills in
/// `policyTypes` when a policy is written without it.  A policy read
/// from a file may lack it, and then the policy isolates ingress, and
/// egress too when it lists egress rules, as the API server would fill
/// it in.
fn types(policy: &NetworkPolicy) -> (bool, bool) {
    let spec = policy.spec.as_ref();
    match spec.and_then(|spec| spec.policy_types.as_ref()) {
        Some(types) if !types.is_empty() => (
            types.iter().any(|kind| kind == "Ingress"),
            types.iter().any(|kind| kind == "Egress"),
        ),
        _ => (
            true,
            spec.and_then(|spec| spec.egress.as_ref())
                .is_some_and(|rules| !rules.is_empty()),
        ),
    }
}

fn apply(applied: &mut Applied, selected: &[usize], rules: &[usize]) {
    for &member in selected {
        applied[member].get_or_insert_with(Vec::new).extend(rules);
    }
}

/// What a peer is matched in: the policy's namespace, every member, the
/// labels of every namespace, and the members in each, so a selector
/// reads only the namespaces it can match.
struct Scope<'s> {
    namespace: &'s str,
    members: &'s [Member],
    labels: &'s BTreeMap<&'s str, &'s BTreeMap<String, String>>,
    by_namespace: &'s HashMap<&'s str, Vec<usize>>,
}

impl<'a> Compiled<'a> {
    /// Adds each of `rules`, given as its peers and ports, and returns
    /// their indexes.
    fn add(
        &mut self,
        policy: &'a str,
        rules: impl Iterator<
            Item = (
                Option<&'a [NetworkPolicyPeer]>,
                Option<&'a [NetworkPolicyPort]>,
            ),
        >,
        scope: &Scope,
    ) -> Vec<usize> {
        let mut added = Vec::new();
        for (peers, ports) in rules {
            let peers = peers.unwrap_or_default();
            let rule = Rule {
                policy,
                peers: (!peers.is_empty()).then(|| matching(peers, scope)),
                blocks: peers
                    .iter()
                    .filter_map(|peer| peer.ip_block.as_ref())
                    .collect(),
                whole: whole(peers, scope),
                everywhere: peers.is_empty() || peers.iter().any(everywhere),
                ports: ports.unwrap_or_default(),
            };
            added.push(self.rules.len());
            self.rules.push(rule);
        }
        added
    }
}

/// The members any of `peers` matches, sorted.
fn matching(peers: &[NetworkPolicyPeer], scope: &Scope) -> Vec<usize> {
    let mut found: HashSet<usize> = HashSet::new();
    for peer in peers {
        found.extend(peer_matches(peer, scope));
    }
    let mut found: Vec<usize> = found.into_iter().collect();
    found.sort_unstable();
    found
}

/// The namespaces `peers` admit every pod of, sorted.
fn whole(peers: &[NetworkPolicyPeer], scope: &Scope) -> Vec<String> {
    let mut found: Vec<String> = peers
        .iter()
        .filter(|peer| peer.ip_block.is_none())
        .filter(|peer| peer.pod_selector.is_some() || peer.namespace_selector.is_some())
        .filter(|peer| peer.pod_selector.as_ref().is_none_or(selects_everything))
        .flat_map(|peer| namespaces(peer, scope))
        .map(str::to_string)
        .collect();
    found.sort();
    found.dedup();
    found
}

/// Whether `peer` admits every pod of every namespace.
fn everywhere(peer: &NetworkPolicyPeer) -> bool {
    peer.ip_block.is_none()
        && peer
            .namespace_selector
            .as_ref()
            .is_some_and(selects_everything)
        && peer.pod_selector.as_ref().is_none_or(selects_everything)
}

fn selects_everything(selector: &LabelSelector) -> bool {
    selector
        .match_labels
        .as_ref()
        .is_none_or(BTreeMap::is_empty)
        && selector
            .match_expressions
            .as_ref()
            .is_none_or(Vec::is_empty)
}

/// The namespaces a peer's namespace selector matches: the policy's own
/// when it has none.
fn namespaces<'s>(peer: &NetworkPolicyPeer, scope: &Scope<'s>) -> Vec<&'s str> {
    match &peer.namespace_selector {
        None => vec![scope.namespace],
        Some(selector) => {
            let labels = scope.labels.iter();
            let found = labels.filter(|(_, labels)| selector::matches(selector, labels));
            found.map(|(&name, _)| name).collect()
        }
    }
}

/// The members one peer matches.  A pod selector alone matches pods in
/// the policy's own namespace, a namespace selector alone matches every
/// pod of the namespaces it matches, and the two together match the
/// pods that the pod selector matches in those namespaces.  An address
/// block matches a member by its pods' addresses, because a connection
/// identifies a pod to the network only by its address.  A selector
/// matches no pod on its machine's network, whose calls come from its
/// machine's address, so only a block that contains the address admits
/// it.
fn peer_matches(peer: &NetworkPolicyPeer, scope: &Scope) -> Vec<usize> {
    if let Some(block) = &peer.ip_block {
        let Some(cidr) = Block::parse(&block.cidr) else {
            return Vec::new();
        };
        let except: Vec<Block> = block
            .except
            .iter()
            .flatten()
            .filter_map(|text| Block::parse(text))
            .collect();
        let inside = |address: &std::net::IpAddr| {
            cidr.contains(*address) && !except.iter().any(|block| block.contains(*address))
        };
        let members = scope.members.iter().enumerate();
        return members
            .filter(|(_, member)| member.addresses.iter().any(inside))
            .map(|(index, _)| index)
            .collect();
    }
    if peer.pod_selector.is_none() && peer.namespace_selector.is_none() {
        return Vec::new();
    }
    let candidates = namespaces(peer, scope)
        .into_iter()
        .flat_map(|namespace| scope.by_namespace.get(namespace).into_iter().flatten());
    candidates
        .copied()
        .filter(|&index| !scope.members[index].host_network)
        .filter(|&index| {
            peer.pod_selector
                .as_ref()
                .is_none_or(|selector| selector::matches(selector, &scope.members[index].labels))
        })
        .collect()
}
