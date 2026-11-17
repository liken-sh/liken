//! Which callers one ingress rule admits to a workload, and whether
//! the rule admits any caller at all.  A rule admits any caller when
//! it lists no peers, or when it has an address block of every address
//! with no exceptions.

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
    /// Anything at all: a rule that lists no peers.
    Anything,
}

/// One ingress rule that admits callers to a workload.  It records the
/// policy that contains the rule, the ports the rule admits, and the
/// callers: workloads, address blocks, or anything.  It also records
/// the namespaces the rule admits whole, and whether the rule admits
/// every namespace.  A caller's own egress rules may still stop a
/// caller that is listed here, and `Reach::Isolated` lists what both
/// ends admit.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Admission {
    pub policy: String,
    pub ports: Ports,
    pub callers: BTreeSet<Caller>,
    pub whole: Vec<String>,
    pub everywhere: bool,
}

/// The address blocks that contain every address.
const EVERY_ADDRESS: [&str; 2] = ["0.0.0.0/0", "::/0"];

impl Caller {
    /// Whether it is any caller at all.
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
    /// Whether it admits any caller at all on `port`.  When `port` is
    /// `None`, it must admit them on every port.
    pub fn admits_anyone_on(&self, port: Option<u16>) -> bool {
        self.callers.iter().any(Caller::is_anyone) && self.covers(port)
    }

    /// Whether it admits the pods of `owner`, in `namespace`, on `port`:
    /// by name, as every pod of their namespace, or as every namespace.
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

/// What `rule` admits to a member of `owner` on `ports`.
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
