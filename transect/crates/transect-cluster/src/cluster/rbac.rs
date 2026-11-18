//! Watches on the RBAC roles and bindings, which `transect` reads to
//! find which controller may write each custom kind's status.
//!
//! Every cluster serves these kinds, but the `ServiceAccount` that
//! `transect` runs as may not be allowed to list some of them, such as
//! `Role`s in all namespaces. So a background probe lists one object of
//! each kind, and retries after a transient failure. A kind has no
//! store until its probe succeeds, and while it has none, the cards say
//! that controllers may be missing.

use std::fmt::Debug;
use std::sync::Arc;

use k8s_openapi::api::rbac::v1::{ClusterRole, ClusterRoleBinding, Role, RoleBinding};
use kube::api::ListParams;
use kube::runtime::reflector::Store;
use kube::{Api, Client, Resource};
use serde::de::DeserializeOwned;

use super::discovery::{Optional, Refusal, State};
use super::reflect::{Changes, reflect};
use crate::ownership::{Grants, Rbac};

#[derive(Clone)]
pub struct RbacStores {
    cluster_roles: Optional<Store<ClusterRole>>,
    roles: Optional<Store<Role>>,
    cluster_role_bindings: Optional<Store<ClusterRoleBinding>>,
    role_bindings: Optional<Store<RoleBinding>>,
}

impl RbacStores {
    pub fn discover(client: &Client, changes: &Changes) -> Self {
        Self {
            cluster_roles: start(client, changes, "ClusterRoles"),
            roles: start(client, changes, "Roles"),
            cluster_role_bindings: start(client, changes, "ClusterRoleBindings"),
            role_bindings: start(client, changes, "RoleBindings"),
        }
    }

    /// Resolves once the probe of each RBAC kind has finished or is
    /// retrying. A store that started has its first full list by then,
    /// unless that list took longer than `ATTEMPT` in `discovery`.
    pub async fn synced(&self) {
        self.cluster_roles.settled().await;
        self.roles.settled().await;
        self.cluster_role_bindings.settled().await;
        self.role_bindings.settled().await;
    }

    /// Who may write each custom kind's status.
    pub fn grants(&self) -> Grants {
        let (cluster_roles, roles) = (state(&self.cluster_roles), state(&self.roles));
        let cluster_role_bindings = state(&self.cluster_role_bindings);
        let role_bindings = state(&self.role_bindings);
        let missing = [
            (cluster_roles.is_none(), "ClusterRoles"),
            (roles.is_none(), "Roles"),
            (cluster_role_bindings.is_none(), "ClusterRoleBindings"),
            (role_bindings.is_none(), "RoleBindings"),
        ];
        let (cluster_roles, roles) = (cluster_roles.unwrap_or_default(), roles.unwrap_or_default());
        let cluster_role_bindings = cluster_role_bindings.unwrap_or_default();
        let role_bindings = role_bindings.unwrap_or_default();
        Grants::build(Rbac {
            cluster_roles: cluster_roles.iter().map(AsRef::as_ref).collect(),
            roles: roles.iter().map(AsRef::as_ref).collect(),
            cluster_role_bindings: cluster_role_bindings.iter().map(AsRef::as_ref).collect(),
            role_bindings: role_bindings.iter().map(AsRef::as_ref).collect(),
            missing: missing
                .into_iter()
                .filter_map(|(missing, kind)| missing.then_some(kind))
                .collect(),
        })
    }
}

/// Every role and binding `transect` may list, for the pages of each.
#[derive(Clone, Default)]
pub struct RbacObjects {
    pub cluster_roles: Vec<Arc<ClusterRole>>,
    pub roles: Vec<Arc<Role>>,
    pub cluster_role_bindings: Vec<Arc<ClusterRoleBinding>>,
    pub role_bindings: Vec<Arc<RoleBinding>>,
}

impl RbacStores {
    /// Every role and binding, and none of a kind that has no store.
    pub fn objects(&self) -> RbacObjects {
        RbacObjects {
            cluster_roles: state(&self.cluster_roles).unwrap_or_default(),
            roles: state(&self.roles).unwrap_or_default(),
            cluster_role_bindings: state(&self.cluster_role_bindings).unwrap_or_default(),
            role_bindings: state(&self.role_bindings).unwrap_or_default(),
        }
    }
}

fn start<K>(client: &Client, changes: &Changes, what: &'static str) -> Optional<Store<K>>
where
    K: Resource<DynamicType = ()>
        + Clone
        + DeserializeOwned
        + serde::Serialize
        + Debug
        + Send
        + Sync
        + 'static,
{
    let (probing, starting) = (client.clone(), client.clone());
    let started = changes.clone();
    Optional::discover(
        what,
        &changes.sender,
        move || listable::<K>(probing.clone()),
        move |()| reflect(Api::all(starting), started),
    )
}

/// Lists one object, so that a kind `transect` may not list is found
/// here, before a watch starts that would fail on every retry.
async fn listable<K>(client: Client) -> Result<(), Refusal>
where
    K: Resource<DynamicType = ()> + Clone + DeserializeOwned + Debug,
{
    let api: Api<K> = Api::all(client);
    api.list(&ListParams::default().limit(1))
        .await
        .inspect_err(|error| {
            tracing::warn!(kind = %K::kind(&()), %error, "Listing this RBAC kind did not complete, so transect finds no controller from its grants");
        })?;
    Ok(())
}

/// Every object of an RBAC kind, or `None` when `transect` has no store
/// for that kind.
fn state<K>(store: &Optional<Store<K>>) -> Option<Vec<Arc<K>>>
where
    K: Resource<DynamicType = ()> + Clone + Send + Sync + 'static,
{
    match store.state() {
        State::On(store) => Some(store.state()),
        _ => None,
    }
}
