//! The callers that one ingress rule admits to a workload, and whether
//! the rule admits every caller. A rule admits every caller when it
//! lists no peers, or when it has an address block that contains every
//! address and has no exceptions.

use std::collections::BTreeSet;

use k8s_openapi::api::networking::v1::IPBlock;

use super::Ports;
use super::members::Member;
use transect_graph::edges::Object;

/// Who may call one target.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Caller {
    /// A workload, by its top owner and the namespace its pods run in.
    Workload { namespace: String, owner: Object },
    /// Addresses in a block, which also admits callers from outside the
    /// cluster.
    Addresses { cidr: String, except: Vec<String> },
    /// Any caller: a rule that lists no peers.
    Anything,
}

/// One ingress rule that admits callers to a workload, as the rule
/// states it. A caller's own egress rules can still refuse the call, so
/// `Reach::Isolated` lists only the callers that both ends admit.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Admission {
    pub policy: String,
    pub ports: Ports,
    pub callers: BTreeSet<Caller>,
    /// The namespaces whose every pod the rule admits.
    pub whole: Vec<String>,
    /// Whether the rule admits every pod of every namespace.
    pub everywhere: bool,
}

/// The address blocks that contain every address.
const EVERY_ADDRESS: [&str; 2] = ["0.0.0.0/0", "::/0"];

impl Caller {
    /// Whether this caller matches every source.
    pub fn is_anyone(&self) -> bool {
        match self {
            Self::Anything => true,
            Self::Addresses { cidr, except } => {
                except.is_empty() && EVERY_ADDRESS.contains(&cidr.as_str())
            }
            Self::Workload { .. } => false,
        }
    }
}

impl Admission {
    /// Whether the rule admits every caller on `port`. When `port` is
    /// `None`, the rule must admit every caller on every port.
    pub fn admits_anyone_on(&self, port: Option<u16>) -> bool {
        self.callers.iter().any(Caller::is_anyone) && self.covers(port)
    }

    /// Whether the rule admits the pods of `owner`, in `namespace`, on
    /// `port`: because it lists the workload, admits any caller, admits
    /// every pod of `namespace`, or admits every namespace. When `port`
    /// is `None`, the rule must admit them on every port.
    pub fn admits_workload(&self, owner: &Object, namespace: &str, port: Option<u16>) -> bool {
        let named = self.callers.iter().any(|caller| match caller {
            Caller::Workload {
                owner: admitted, ..
            } => admitted == owner,
            Caller::Anything => true,
            Caller::Addresses { .. } => false,
        });
        let whole = self.everywhere || self.whole.iter().any(|w| w == namespace);
        (named || whole) && self.covers(port)
    }

    fn covers(&self, port: Option<u16>) -> bool {
        match port {
            Some(port) => self.ports.covers(port),
            None => self.ports == Ports::Any,
        }
    }
}

/// The `Admission` of `rule` for a member of `owner`, on `ports`. The
/// callers leave out `owner`'s own pods.
pub(super) fn admission(
    rule: &super::rules::Rule,
    members: &[Member],
    owner: &Object,
    ports: Ports,
) -> Admission {
    let mut callers: BTreeSet<Caller> = BTreeSet::new();
    match &rule.peers {
        None => {
            callers.insert(Caller::Anything);
        }
        Some(peers) => {
            let others = peers.iter().map(|&peer| &members[peer]);
            let others = others.filter(|member| &member.owner != owner);
            callers.extend(others.map(|member| Caller::Workload {
                namespace: member.namespace.clone(),
                owner: member.owner.clone(),
            }));
        }
    }
    callers.extend(rule.blocks.iter().map(|block| addresses(block)));
    Admission {
        policy: rule.policy.to_string(),
        ports,
        callers,
        whole: rule.whole.clone(),
        everywhere: rule.everywhere,
    }
}

pub(super) fn addresses(block: &IPBlock) -> Caller {
    Caller::Addresses {
        cidr: block.cidr.clone(),
        except: block.except.clone().unwrap_or_default(),
    }
}
