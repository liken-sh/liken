//! The access lens shows what may reach each port that a workload
//! serves, and what each account may do in which namespaces.
//!
//! The lens computes access only from Roles, ClusterRoles,
//! RoleBindings, ClusterRoleBindings, ServiceAccounts, and pod specs.
//! It makes no SubjectAccessReview and reads no Secret, so a Secret
//! shows only by the name that a pod spec or a Role gives it.  A
//! webhook authorizer, the node authorizer, and admission webhooks can
//! allow or refuse requests that RBAC does not, and the lens cannot see
//! them, so the pages state this limit.

pub mod account;
pub mod accounts;
mod capability;
mod changers;
pub mod drawing;
mod follows;
mod grants;
pub mod namespace;
pub mod presents;
pub mod routes;
pub mod rules;
mod running;
#[cfg(test)]
mod tests;
pub mod ways;

use std::collections::{BTreeMap, BTreeSet, HashMap};

use k8s_openapi::api::rbac::v1::PolicyRule;

pub use account::{Account, AppliesAs, Hop, Via, Where};
pub use accounts::{AccountsPage, Look};
pub use capability::{ALL, Capability, GitOpsKind, Scope};
pub use changers::Changers;
pub use running::Running;
pub use ways::listeners::parse as parse_load_balancer;
pub use ways::{LoadBalancerController, WaysPage};

use transect_cluster::cluster::RbacObjects;
use transect_graph::edges::Object;

/// Whether a workload serves a port, and so has an access tab.  A
/// workload serves a port when its pods declare one, when its pods run
/// on their machine's network, or when a Service selects it.
pub fn serves(workload: &transect_cluster::workloads::Workload, selected: bool) -> bool {
    let specs = workload.templates();
    let declared = specs.iter().any(|spec| {
        spec.host_network == Some(true)
            || spec
                .containers
                .iter()
                .any(|c| c.ports.as_ref().is_some_and(|ports| !ports.is_empty()))
    });
    selected || declared
}

pub fn link(object: &Object, href: &dyn Fn(&Object) -> Option<String>) -> Option<String> {
    href(object)
}

/// The objects that [`Access::build`] reads.
pub struct Inputs<'a> {
    pub rbac: &'a RbacObjects,
    /// Every ServiceAccount in the store, as (namespace, name).
    pub service_accounts: Vec<(String, String)>,
    /// The ServiceAccount and mounts of each pod that has not finished.
    pub pods: Vec<Running>,
    /// The kinds whose change is the `ChangeApplied` capability.
    pub gitops: Vec<GitOpsKind>,
    /// Every object that applies other objects, with the accounts it
    /// applies them as.
    pub appliers: Vec<Applier>,
}

/// An object that applies other objects, such as a Flux
/// `Kustomization`, and the accounts it applies them as.  When its
/// kind's `KindKnowledge` names the field that holds a ServiceAccount,
/// the object applies as that ServiceAccount.  Otherwise it applies as
/// its controller's accounts, which are the accounts that may write the
/// status of its kind.
#[derive(Clone, Debug, Hash, PartialEq, Eq, PartialOrd, Ord)]
pub struct Applier {
    pub object: Object,
    /// The plural of its kind, as an RBAC rule names it.
    pub plural: String,
    pub accounts: Vec<Account>,
    /// Whether the object names its account, instead of applying as its
    /// controller's accounts.
    pub named: bool,
    /// The field that names its ServiceAccount, when the `KindKnowledge`
    /// gives one.  An account that may change the object can set this
    /// field to any ServiceAccount in the object's namespace.
    pub field: Option<String>,
    /// What it applies as when the field names no account: the field's
    /// default, or else its controller's accounts.
    pub fallback: Vec<Account>,
    /// The source object it fetches from, and the plural of the
    /// source's kind.  An account that may change the source changes
    /// what this object applies, even from another namespace.
    pub source: Option<(Object, String)>,
}

/// What every account may do, and where.
#[derive(Debug, Default)]
pub struct Access {
    pub accounts: BTreeMap<Account, Held>,
    /// For each kind, the accounts that a binding lets change it, and
    /// where.
    pub changers: Changers,
    /// The rules of each role, for drawing an account bound to it.
    pub rules: HashMap<Object, Vec<PolicyRule>>,
    /// The kinds that the `ChangeApplied` capability covers.
    pub gitops: Vec<GitOpsKind>,
}

impl Access {
    pub fn build(inputs: &Inputs) -> Self {
        let (mut accounts, changers) = grants::granted(inputs);
        follows::close(&mut accounts, inputs);
        Self {
            accounts,
            changers,
            rules: grants::rules(inputs.rbac),
            gitops: inputs.gitops.clone(),
        }
    }

    pub fn of(&self, account: &Account) -> Option<&Held> {
        self.accounts.get(account)
    }
}

/// What one account may do, and the bindings and roles that allow it.
#[derive(Clone, Debug, Default)]
pub struct Held {
    pub cells: BTreeMap<Capability, Cell>,
    /// The bindings that name the account itself, not a group it is in.
    pub bound: Vec<Bound>,
    /// The roles bound to the account that allow every verb on every
    /// resource, each with where its binding applies.
    pub everything: BTreeSet<(Where, Object)>,
    /// Every role a binding gives the account, directly or through a
    /// group, and where the binding applies.
    pub roles: BTreeSet<(Where, Object)>,
    /// The routes by which it may act as another account, where each
    /// applies, and the object names each is limited to.  An empty list
    /// of names means every object.
    pub routes: BTreeMap<(routes::Route, Where), routes::Names>,
    /// The GitOps kinds it may create, patch, or update, where, and the
    /// object names each is limited to.
    pub writes: BTreeMap<(GitOpsKind, Where), routes::Names>,
}

impl Held {
    /// Where it has `capability` on every object, from a binding or
    /// from a `Follow`.
    pub fn unnamed(&self, capability: Capability) -> Vec<Where> {
        let cell = self.cells.get(&capability);
        cell.map(Cell::unnamed_places).unwrap_or_default()
    }

    /// Whether it has any capability anywhere.
    pub fn holds_any(&self) -> bool {
        self.cells.values().any(|cell| !cell.is_empty())
    }
}

/// One binding that names an account.  `defaults` is true when the
/// binding has the label `kubernetes.io/bootstrapping: rbac-defaults`,
/// which Kubernetes puts on the bindings it creates.  `grants` is true
/// when the role grants any capability.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Bound {
    pub binding: Object,
    pub role: Object,
    pub defaults: bool,
    pub grants: bool,
}

/// One capability of one account: where a binding grants it, and where
/// the account gets it by using another capability, which `follows`
/// records.
#[derive(Clone, Debug, Default)]
pub struct Cell {
    pub granted: BTreeMap<Where, Vec<Via>>,
    pub follows: BTreeMap<Where, Follow>,
}

/// A capability that an account gets with no binding that grants it:
/// the first hop of the chain that leads to it, and the object names it
/// is limited to.  An empty list of names means every object.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Follow {
    pub hop: Hop,
    pub names: Vec<String>,
}

impl Cell {
    pub fn is_empty(&self) -> bool {
        self.granted.is_empty() && self.follows.is_empty()
    }

    /// Where a binding grants it to every object.
    pub fn granted_unnamed(&self) -> impl Iterator<Item = &Where> {
        let every = |(_, vias): &(&Where, &Vec<Via>)| vias.iter().any(|via| via.names.is_empty());
        self.granted.iter().filter(every).map(|(at, _)| at)
    }

    /// Where it covers every object, from a binding or a `Follow`.
    pub fn unnamed_places(&self) -> Vec<Where> {
        let followed = self.follows.iter().filter(|(_, f)| f.names.is_empty());
        let mut places: BTreeSet<Where> = self.granted_unnamed().cloned().collect();
        places.extend(followed.map(|(at, _)| at.clone()));
        places.into_iter().collect()
    }

    /// Whether it already covers every object at `place`, or in every
    /// namespace.
    fn holds(&self, place: &Where) -> bool {
        [place, &Where::Everywhere].into_iter().any(|at| {
            let granted = self.granted.get(at);
            let granted = granted.is_some_and(|vias| vias.iter().any(|via| via.names.is_empty()));
            granted || self.follows.get(at).is_some_and(|f| f.names.is_empty())
        })
    }

    /// Adds `follow` at `place` and returns whether it added it.  It
    /// adds nothing where the cell already covers every object.  A
    /// follow limited by name is added only where the cell has no entry
    /// at `place`, and a follow for every object replaces one limited by
    /// name.
    fn follow(&mut self, place: Where, follow: Follow) -> bool {
        if self.holds(&place) {
            return false;
        }
        let named = !follow.names.is_empty();
        if named && (self.granted.contains_key(&place) || self.follows.contains_key(&place)) {
            return false;
        }
        self.follows.insert(place, follow);
        true
    }
}
