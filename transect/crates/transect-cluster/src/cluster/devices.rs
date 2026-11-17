//! Watches on ResourceClaims and ResourceSlices, when the cluster serves
//! `resource.k8s.io/v1`.
//!
//! Discovery runs in the background from the start, and again after a
//! transient failure until it has an answer.  The `holds` edge needs
//! both kinds, a claim for what a pod holds and a slice for where the
//! device is, so a cluster missing either gets neither store.

use std::sync::Arc;

use k8s_openapi::api::resource::v1::{ResourceClaim, ResourceSlice};
use kube::api::ListParams;
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Ready, Refusal, State};
use super::reflect::{Changes, reflect};

const VERSION: &str = "resource.k8s.io/v1";

/// The ResourceClaims and ResourceSlices, once discovery finds both.
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
    /// Starts finding whether the cluster serves both kinds and `transect`
    /// may list them, and a watch on each once found.
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

    /// Resolves once discovery has first answered, and the stores, when
    /// it found the kinds, hold their first full list.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every claim and slice as of now, or the state of discovery.
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
    /// Every ResourceClaim as of now, or none while the kind is off.
    pub fn claims(&self) -> Vec<Arc<ResourceClaim>> {
        match self.0.state() {
            State::On(stores) => stores.claims.state(),
            _ => Vec::new(),
        }
    }
}

/// Whether the API serves both kinds at `v1`, and a list of one of each
/// succeeds, so a kind `transect` may not list is found by the probe
/// instead of as a watch that fails forever.
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
        tracing::warn!(%error, "cannot list ResourceClaims");
    })?;
    slices.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "cannot list ResourceSlices");
    })?;
    Ok(())
}
