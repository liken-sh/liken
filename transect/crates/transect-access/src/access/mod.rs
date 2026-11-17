//! The access lens shows what may reach each port, what each account
//! may do, and where the account may do it.  It reads only the
//! cluster's objects.
//!
//! The lens computes access from Roles, ClusterRoles, RoleBindings, and
//! ClusterRoleBindings, the ServiceAccounts' metadata, and the pods'
//! specs.  It makes no SubjectAccessReview and reads no Secret, so a
//! Secret shows only by the name that a pod spec or a Role gives it.
//! RBAC does not show a webhook authorizer, the node authorizer, or an
//! admission webhook, and the pages state this limit.

pub mod account;
pub mod accounts;
mod capability;
mod changers;
pub mod drawing;
mod follows;
mod grants;
pub mod namespace;
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

/// The page a name on an access page links to, as `href` gives it.
pub fn link(object: &Object, href: &dyn Fn(&Object) -> Option<String>) -> Option<String> {
    href(object)
}

/// The objects that [`Access::build`] reads.
pub struct Inputs<'a> {
    pub rbac: &'a RbacObjects,
    /// Every ServiceAccount the store holds, by namespace and name.
    pub service_accounts: Vec<(String, String)>,
    /// What each pod that has not finished runs as and mounts.
    pub pods: Vec<Running>,
    /// The kinds whose change is the sixth capability.
    pub gitops: Vec<GitOpsKind>,
    /// Every object that applies, with the accounts it applies as.
    pub appliers: Vec<Applier>,
}

/// An object that applies what it names, and the accounts it applies
/// as.  When its kind's knowledge gives the field that names a
/// ServiceAccount, the object applies as that ServiceAccount.
/// Otherwise it applies as its controller's accounts, which are the
/// accounts that may write its kind's status, as controller
/// attribution finds them.
#[derive(Clone, Debug, Hash, PartialEq, Eq, PartialOrd, Ord)]
pub struct Applier {
    pub object: Object,
    /// Its kind's plural, by which a rule names the kind.
    pub plural: String,
    /// The accounts it applies as now.
    pub accounts: Vec<Account>,
    /// Whether the object names the account, rather than applying as
    /// its controller's.
    pub named: bool,
    /// The field that names the account it applies as, when its kind's
    /// knowledge gives one.  An account that may change the object may
    /// set the field to any ServiceAccount in the object's namespace.
    pub field: Option<String>,
    /// What it applies as when the field names no account: the field's
    /// default, or else its controller's accounts.
    pub fallback: Vec<Account>,
    /// The object it fetches through, and the plural of that object's
    /// kind.  An account that changes the source changes what this
    /// object applies, from any namespace.
    pub source: Option<(Object, String)>,
}

/// What every account may do, and where.
#[derive(Debug, Default)]
pub struct Access {
    pub accounts: BTreeMap<Account, Held>,
    /// Who a binding lets change each kind, and where.
    pub changers: Changers,
    /// Each role's rules, by the role, for the drawing of an account
    /// bound to it.
    pub rules: HashMap<Object, Vec<PolicyRule>>,
    /// The kinds the sixth capability reads.
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

/// What one account may do, and the bindings and roles that let it.
#[derive(Clone, Debug, Default)]
pub struct Held {
    pub cells: BTreeMap<Capability, Cell>,
    /// The bindings that name it directly, and not a group it is in.
    pub bound: Vec<Bound>,
    /// Where it is bound to a role with every verb on every resource,
    /// and the role.
    pub everything: BTreeSet<(Where, Object)>,
    /// Every role a binding binds it to, directly or through a group,
    /// and where the binding applies.
    pub roles: BTreeSet<(Where, Object)>,
    /// The rules that let it act as another account, where its binding
    /// applies, with the names each is limited to, or none for every
    /// object.
    pub routes: BTreeMap<(routes::Route, Where), routes::Names>,
    /// The GitOps kinds it may create, patch, or update, where, with
    /// the names each is limited to.
    pub writes: BTreeMap<(GitOpsKind, Where), routes::Names>,
}

impl Held {
    /// Where it may do `capability` to every object, granted or not.
    pub fn unnamed(&self, capability: Capability) -> Vec<Where> {
        let cell = self.cells.get(&capability);
        cell.map(Cell::unnamed_places).unwrap_or_default()
    }

    /// Whether it has any of the six capabilities anywhere.
    pub fn holds_any(&self) -> bool {
        self.cells.values().any(|cell| !cell.is_empty())
    }
}

/// One binding that names an account: the binding, its role, whether
/// the binding is one Kubernetes ships, and whether its role grants any
/// of the six.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Bound {
    pub binding: Object,
    pub role: Object,
    pub defaults: bool,
    pub grants: bool,
}

/// One capability of one account: where a binding grants it, and where
/// it follows from a grant.
#[derive(Clone, Debug, Default)]
pub struct Cell {
    pub granted: BTreeMap<Where, Vec<Via>>,
    pub follows: BTreeMap<Where, Follow>,
}

/// A capability that follows, its first hop, and the objects it is
/// limited to, by name, or none for every object.
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

    /// Where it applies to every object, by a grant or by a follow.
    pub fn unnamed_places(&self) -> Vec<Where> {
        let followed = self.follows.iter().filter(|(_, f)| f.names.is_empty());
        let mut places: BTreeSet<Where> = self.granted_unnamed().cloned().collect();
        places.extend(followed.map(|(at, _)| at.clone()));
        places.into_iter().collect()
    }

    /// Whether it already applies to every object at `place`, or to
    /// every object everywhere.
    fn holds(&self, place: &Where) -> bool {
        [place, &Where::Everywhere].into_iter().any(|at| {
            let granted = self.granted.get(at);
            let granted = granted.is_some_and(|vias| vias.iter().any(|via| via.names.is_empty()));
            granted || self.follows.get(at).is_some_and(|f| f.names.is_empty())
        })
    }

    /// Adds `follow` at `place` unless the cell already applies to
    /// every object there, and returns whether it added it.  A follow
    /// limited by name is added only where the cell has no entry at
    /// `place`, and a follow for every object replaces one limited by
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
