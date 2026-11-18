//! What each binding grants to which accounts.  The module reduces the
//! rules of every role to the capabilities in `capability::ALL`, then
//! adds each binding's grant to the accounts that its subjects name and
//! to the members of the groups that its subjects name.

use std::collections::{BTreeMap, BTreeSet, HashMap};

use k8s_openapi::api::rbac::v1::{PolicyRule, Subject};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::account::{AUTHENTICATED, Account, SERVICE_ACCOUNTS, Via, Where};
use super::capability::{self, ALL, Capability, GitOpsKind, Scope};
use super::changers::{Changed, Changers, changed};
use super::routes::{self, Limit, Names, Route};
use super::{Bound, Held, Inputs};
use transect_graph::edges::Object;

const RBAC: &str = "rbac.authorization.k8s.io";

/// The label that Kubernetes puts on the roles and bindings that it
/// creates by default.
const DEFAULTS: (&str, &str) = ("kubernetes.io/bootstrapping", "rbac-defaults");

/// Each capability that a role's rules grant, with the object names
/// that a rule limits it to, or an empty list for every object.
type Capabilities = BTreeMap<Capability, Vec<String>>;

/// What one role's rules grant.
#[derive(Default)]
pub(super) struct RoleGrants {
    /// What it grants through a RoleBinding, in the binding's
    /// namespace.
    pub(super) in_namespace: Capabilities,
    /// What it grants through a ClusterRoleBinding, which also
    /// authorizes cluster-scoped resources such as `nodes/proxy`.
    pub(super) in_cluster: Capabilities,
    /// The routes to another account that it grants through a
    /// RoleBinding, and through a ClusterRoleBinding.
    pub(super) routes_in_namespace: BTreeMap<Route, Names>,
    pub(super) routes_in_cluster: BTreeMap<Route, Names>,
    /// Whether a rule allows every verb on every resource.
    pub(super) everything: bool,
    /// The GitOps kinds that its rules let a subject create, patch, or
    /// update, with the names that a rule limits each to.
    pub(super) writes: BTreeMap<GitOpsKind, Names>,
    /// The kinds that its rules let a subject change.
    changes: Vec<Changed>,
}

impl RoleGrants {
    fn read(rules: Option<&[PolicyRule]>, gitops: &[GitOpsKind]) -> Self {
        let rules = rules.unwrap_or_default();
        Self {
            in_namespace: capabilities(rules, gitops, Scope::Namespace),
            in_cluster: capabilities(rules, gitops, Scope::Cluster),
            routes_in_namespace: routes::read(rules, Scope::Namespace),
            routes_in_cluster: routes::read(rules, Scope::Cluster),
            writes: writes(rules, gitops),
            everything: rules.iter().any(capability::everything),
            changes: changed(rules),
        }
    }

    /// Whether it grants any capability or route, or everything,
    /// through a binding that applies at `at`.
    fn grants_any(&self, at: &Where) -> bool {
        !self.at(at).is_empty() || !self.routes_at(at).is_empty() || self.everything
    }

    /// The routes that it grants through a binding that applies at
    /// `at`.
    pub(super) fn routes_at(&self, at: &Where) -> &BTreeMap<Route, Names> {
        match at {
            Where::Everywhere => &self.routes_in_cluster,
            Where::In(_) => &self.routes_in_namespace,
        }
    }

    /// The capabilities that it grants through a binding that applies
    /// at `at`.
    pub(super) fn at(&self, at: &Where) -> &Capabilities {
        match at {
            Where::Everywhere => &self.in_cluster,
            Where::In(_) => &self.in_namespace,
        }
    }
}

/// Each kind in `gitops` that `rules` let a subject create, patch, or
/// update, with the names that the rules limit it to.
fn writes(rules: &[PolicyRule], gitops: &[GitOpsKind]) -> BTreeMap<GitOpsKind, Names> {
    let mut found = BTreeMap::new();
    for kind in gitops {
        let mut limit = Limit::default();
        let (group, plural) = kind;
        limit.add_kind(rules, group, plural, &["create"]);
        limit.add_kind(rules, group, plural, &["patch", "update"]);
        if let Some(names) = limit.names() {
            found.insert(kind.clone(), names);
        }
    }
    found
}

/// Each capability that `rules` grant through a binding of `scope`.
fn capabilities(rules: &[PolicyRule], gitops: &[GitOpsKind], scope: Scope) -> Capabilities {
    let mut found = Capabilities::new();
    for capability in ALL {
        let mut every = false;
        let mut names: BTreeSet<String> = BTreeSet::new();
        for rule in rules {
            match capability::grants(rule, capability, gitops, scope) {
                Some(named) if named.is_empty() => every = true,
                Some(named) => names.extend(named),
                None => {}
            }
        }
        match (every, names.is_empty()) {
            (true, _) => {
                found.insert(capability, Vec::new());
            }
            (false, false) => {
                found.insert(capability, names.into_iter().collect());
            }
            (false, true) => {}
        }
    }
    found
}

/// One RoleBinding or ClusterRoleBinding, reduced to the fields that
/// [`granted`] reads.
struct Binding<'a> {
    object: Object,
    /// Whether it is one of the bindings that Kubernetes creates by
    /// default.
    defaults: bool,
    role: Object,
    at: Where,
    /// The namespace to use for a subject that has none.
    namespace: Option<&'a str>,
    subjects: &'a [Subject],
}

/// Every ServiceAccount in the cluster and every account that a binding
/// names, with what the bindings grant each, and the accounts that the
/// bindings let change each kind.
pub fn granted(inputs: &Inputs) -> (BTreeMap<Account, Held>, Changers) {
    let roles = roles(inputs);
    let mut changers = Changers::default();
    let mut held: BTreeMap<Account, Held> = BTreeMap::new();
    for (namespace, name) in &inputs.service_accounts {
        held.entry(Account::service_account(namespace, name))
            .or_default();
    }
    let bindings = bindings(inputs);
    let mut to_groups: Vec<(String, &Binding, &RoleGrants)> = Vec::new();
    for binding in &bindings {
        let Some(grants) = roles.get(&binding.role) else {
            continue;
        };
        for subject in binding.subjects {
            let Some(account) = account(subject, binding.namespace) else {
                continue;
            };
            if let Account::Group(group) = &account {
                to_groups.push((group.clone(), binding, grants));
            }
            for changed in &grants.changes {
                changers.add(changed, &binding.at, &account);
            }
            let entry = held.entry(account).or_default();
            entry.bound.push(Bound {
                binding: binding.object.clone(),
                role: binding.role.clone(),
                defaults: binding.defaults,
                grants: grants.grants_any(&binding.at),
            });
            give(entry, binding, grants, None);
        }
    }
    let members: Vec<Account> = held
        .keys()
        .filter(|a| !a.groups().is_empty())
        .cloned()
        .collect();
    for (group, binding, grants) in to_groups {
        if !grants.grants_any(&binding.at) {
            continue;
        }
        for member in members
            .iter()
            .filter(|member| member.groups().contains(&group))
        {
            let entry = held.entry(member.clone()).or_default();
            give(entry, binding, grants, Some(&group));
        }
    }
    (held, changers)
}

/// Adds to `held` what `grants` gives through `binding`.  `group` is
/// the group that the account is a member of, when the binding names
/// that group and not the account.
fn give(held: &mut Held, binding: &Binding, grants: &RoleGrants, group: Option<&str>) {
    for (capability, names) in grants.at(&binding.at) {
        let via = Via {
            binding: binding.object.clone(),
            role: binding.role.clone(),
            names: names.clone(),
            group: group.map(Into::into),
        };
        let cell = held.cells.entry(*capability).or_default();
        let vias = cell.granted.entry(binding.at.clone()).or_default();
        if !vias.contains(&via) {
            vias.push(via);
        }
    }
    for (route, names) in grants.routes_at(&binding.at) {
        let key = (*route, binding.at.clone());
        merge(
            held.routes.entry(key).or_insert_with(|| names.clone()),
            names,
        );
    }
    for (kind, names) in &grants.writes {
        let key = (kind.clone(), binding.at.clone());
        merge(
            held.writes.entry(key).or_insert_with(|| names.clone()),
            names,
        );
    }
    if grants.everything {
        held.everything
            .insert((binding.at.clone(), binding.role.clone()));
    }
    held.roles
        .insert((binding.at.clone(), binding.role.clone()));
}

/// Every Role and ClusterRole, by its object, with what its rules
/// grant.
pub(super) fn roles(inputs: &Inputs) -> HashMap<Object, RoleGrants> {
    let rbac = inputs.rbac;
    let cluster = rbac.cluster_roles.iter().filter_map(|role| {
        let name = role.metadata.name.as_deref()?;
        let object = Object::new(RBAC, "ClusterRole", None, name);
        Some((
            object,
            RoleGrants::read(role.rules.as_deref(), &inputs.gitops),
        ))
    });
    let namespaced = rbac.roles.iter().filter_map(|role| {
        let meta = &role.metadata;
        let object = Object::new(
            RBAC,
            "Role",
            meta.namespace.as_deref(),
            meta.name.as_deref()?,
        );
        Some((
            object,
            RoleGrants::read(role.rules.as_deref(), &inputs.gitops),
        ))
    });
    cluster.chain(namespaced).collect()
}

/// The rules of every Role and ClusterRole, by its object.
pub fn rules(rbac: &transect_cluster::cluster::RbacObjects) -> HashMap<Object, Vec<PolicyRule>> {
    let cluster = rbac.cluster_roles.iter().filter_map(|role| {
        let name = role.metadata.name.as_deref()?;
        let object = Object::new(RBAC, "ClusterRole", None, name);
        Some((object, role.rules.clone().unwrap_or_default()))
    });
    let namespaced = rbac.roles.iter().filter_map(|role| {
        let meta = &role.metadata;
        let name = meta.name.as_deref()?;
        let object = Object::new(RBAC, "Role", meta.namespace.as_deref(), name);
        Some((object, role.rules.clone().unwrap_or_default()))
    });
    cluster.chain(namespaced).collect()
}

/// Every binding, with the role it names and where it applies.  A
/// ClusterRoleBinding may name only a ClusterRole, so the function
/// skips one that names a Role.
fn bindings<'a>(inputs: &'a Inputs) -> Vec<Binding<'a>> {
    let rbac = inputs.rbac;
    let cluster = rbac.cluster_role_bindings.iter().filter_map(|binding| {
        let name = binding.metadata.name.as_deref()?;
        let role = &binding.role_ref;
        (role.kind == "ClusterRole").then(|| Binding {
            object: Object::new(RBAC, "ClusterRoleBinding", None, name),
            defaults: shipped(&binding.metadata),
            role: Object::new(RBAC, "ClusterRole", None, &role.name),
            at: Where::Everywhere,
            namespace: None,
            subjects: binding.subjects.as_deref().unwrap_or_default(),
        })
    });
    let namespaced = rbac.role_bindings.iter().filter_map(|binding| {
        let meta = &binding.metadata;
        let (namespace, name) = (meta.namespace.as_deref()?, meta.name.as_deref()?);
        let role = &binding.role_ref;
        let role = match role.kind.as_str() {
            "ClusterRole" => Object::new(RBAC, "ClusterRole", None, &role.name),
            "Role" => Object::new(RBAC, "Role", Some(namespace), &role.name),
            _ => return None,
        };
        Some(Binding {
            object: Object::new(RBAC, "RoleBinding", Some(namespace), name),
            defaults: shipped(meta),
            role,
            at: Where::In(namespace.into()),
            namespace: Some(namespace),
            subjects: binding.subjects.as_deref().unwrap_or_default(),
        })
    });
    cluster.chain(namespaced).collect()
}

/// Whether Kubernetes creates the object that `meta` describes by
/// default.
fn shipped(meta: &ObjectMeta) -> bool {
    let labels = meta.labels.as_ref();
    labels.and_then(|l| l.get(DEFAULTS.0)).map(String::as_str) == Some(DEFAULTS.1)
}

/// The account that `subject` names.  A ServiceAccount subject with no
/// namespace is in `namespace`, the namespace of its RoleBinding.  A
/// User subject named `system:serviceaccount:{namespace}:{name}`, the
/// username that a ServiceAccount authenticates as, is that
/// ServiceAccount.
pub fn account(subject: &Subject, namespace: Option<&str>) -> Option<Account> {
    match subject.kind.as_str() {
        "ServiceAccount" => {
            let namespace = subject.namespace.as_deref().or(namespace)?;
            Some(Account::service_account(namespace, &subject.name))
        }
        "Group" => Some(Account::Group(subject.name.clone())),
        "User" => match subject.name.strip_prefix("system:serviceaccount:") {
            Some(rest) => {
                let (namespace, name) = rest.split_once(':')?;
                Some(Account::service_account(namespace, name))
            }
            None => Some(Account::User(subject.name.clone())),
        },
        _ => None,
    }
}

/// The groups that every account of some kind belongs to:
/// `system:authenticated` has every authenticated account, and
/// `system:serviceaccounts` has every ServiceAccount.  A grant to one
/// of them applies to all of its members.
pub const EVERYONE: [&str; 2] = [AUTHENTICATED, SERVICE_ACCOUNTS];

/// Merges `names`, the object names of one grant, into `limit`, the
/// names of another grant of the same capability.  An empty list means
/// every object, so the result is empty when either list is.
fn merge(limit: &mut Names, names: &Names) {
    if names.is_empty() {
        limit.clear();
    } else if !limit.is_empty() {
        limit.extend(names.iter().cloned());
        limit.sort();
        limit.dedup();
    }
}
