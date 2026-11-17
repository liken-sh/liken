//! Watches on RBAC's roles and bindings, which controller attribution
//! reads to find who may write each custom kind's status.
//!
//! Every cluster serves these kinds, but the `ServiceAccount` that
//! `transect` runs as may be refused some of them, such as Roles across
//! namespaces.  Each kind is tried in the background with a list of one
//! object, and again after a transient failure.  A kind that `transect`
//! may not list, or has not listed yet, gets no store, and the cards
//! state that controllers may be missing because of it.

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

    /// Resolves once each RBAC kind has first answered, and each store
    /// `transect` could start holds its first full list.
    pub async fn synced(&self) {
        self.cluster_roles.settled().await;
        self.roles.settled().await;
        self.cluster_role_bindings.settled().await;
        self.role_bindings.settled().await;
    }

    /// Who may write each custom kind's status, as of now.
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
    /// Every role and binding as of now, with none of a kind `transect`
    /// may not list.
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

/// Tries a list of one object, so a kind `transect` may not list is found
/// by the probe instead of as a watch that fails forever.
async fn listable<K>(client: Client) -> Result<(), Refusal>
where
    K: Resource<DynamicType = ()> + Clone + DeserializeOwned + Debug,
{
    let api: Api<K> = Api::all(client);
    api.list(&ListParams::default().limit(1))
        .await
        .inspect_err(|error| {
            tracing::warn!(kind = %K::kind(&()), %error, "cannot list this RBAC kind; transect finds no controller from its grants");
        })?;
    Ok(())
}

/// Every object of an RBAC kind `transect` watches, or none for one it
/// does not.
fn state<K>(store: &Optional<Store<K>>) -> Option<Vec<Arc<K>>>
where
    K: Resource<DynamicType = ()> + Clone + Send + Sync + 'static,
{
    match store.state() {
        State::On(store) => Some(store.state()),
        _ => None,
    }
}
