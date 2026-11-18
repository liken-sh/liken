//! Watches on `ResourceClaim`s and `ResourceSlice`s, when the cluster
//! serves `resource.k8s.io/v1`.
//!
//! A probe runs in the background from startup, and retries after a
//! transient failure until it succeeds or fails for good. The `holds`
//! edge needs both kinds: the claim gives the device a pod holds, and
//! the slice gives the node that the device is on. So when the cluster
//! lacks either kind, `transect` starts neither store.

use std::sync::Arc;

use k8s_openapi::api::resource::v1::{ResourceClaim, ResourceSlice};
use kube::api::ListParams;
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Ready, Refusal, State};
use super::reflect::{Changes, reflect};

const VERSION: &str = "resource.k8s.io/v1";

/// The `ResourceClaim`s and `ResourceSlice`s, once the probe has found
/// both kinds.
pub type Objects = State<(Vec<Arc<ResourceClaim>>, Vec<Arc<ResourceSlice>>)>;

#[derive(Clone)]
struct Stores {
    claims: Store<ResourceClaim>,
    slices: Store<ResourceSlice>,
}

impl Ready for Stores {
    async fn ready(&self) {
        self.claims.ready().await;
        self.slices.ready().await;
    }
}

#[derive(Clone)]
pub struct DeviceStores(Optional<Stores>);

impl DeviceStores {
    /// Starts probing whether the cluster serves both kinds and
    /// `transect` may list them, and starts a watch on each kind when
    /// the probe succeeds.
    pub fn discover(client: &Client, changes: &Changes) -> Self {
        let (probing, starting) = (client.clone(), client.clone());
        let started = changes.clone();
        Self(Optional::discover(
            "ResourceClaims and ResourceSlices",
            &changes.sender,
            move || served(probing.clone()),
            move |()| Stores {
                claims: reflect(Api::all(starting.clone()), started.clone()),
                slices: reflect(Api::all(starting), started),
            },
        ))
    }

    /// Resolves once the probe has finished or is retrying. When the
    /// probe succeeded, both stores have their first full list by then,
    /// unless that list took longer than `ATTEMPT` in `discovery`.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every claim and slice, or the probe's state when the stores are
    /// not running.
    pub fn objects(&self) -> Objects {
        match self.0.state() {
            State::On(stores) => State::On((stores.claims.state(), stores.slices.state())),
            State::Probing => State::Probing,
            State::Retrying => State::Retrying,
            State::Off(off) => State::Off(off),
        }
    }
}

impl DeviceStores {
    /// Every `ResourceClaim`, or none when the stores are not running.
    pub fn claims(&self) -> Vec<Arc<ResourceClaim>> {
        match self.0.state() {
            State::On(stores) => stores.claims.state(),
            _ => Vec::new(),
        }
    }
}

/// Succeeds when the API server serves both kinds at `v1` and a list of
/// each, limited to one object, succeeds. The list finds a kind that
/// `transect` may not list here, before a watch starts that would fail
/// on every retry.
async fn served(client: Client) -> Result<(), Refusal> {
    let resources = client.list_api_group_resources(VERSION).await?;
    let serves = |kind: &str| resources.resources.iter().any(|r| r.kind == kind);
    if !serves("ResourceClaim") || !serves("ResourceSlice") {
        return Err(Refusal::NotServed);
    }
    let one = ListParams::default().limit(1);
    let claims: Api<ResourceClaim> = Api::all(client.clone());
    let slices: Api<ResourceSlice> = Api::all(client);
    claims.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "Listing ResourceClaims did not complete");
    })?;
    slices.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "Listing ResourceSlices did not complete");
    })?;
    Ok(())
}
