//! A watch on `CustomResourceDefinition`s, each trimmed to the parts
//! that describe its kind, when `transect` may list them.
//!
//! A background probe checks whether `transect` may list CRDs, as it
//! does for `VolumeAttachment`s, so an account that may not list them
//! still gets its map. The cards of custom kinds then use the
//! descriptions from their images instead of the CRDs'.

use std::sync::Arc;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kube::api::ListParams;
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Refusal, State};
use super::reflect::{Changes, reflect_cut};
use super::trim;

#[derive(Clone)]
pub struct DefinitionStore(Optional<Store<CustomResourceDefinition>>);

impl DefinitionStore {
    /// Starts probing whether `transect` may list CRDs, and starts a
    /// watch when it may.
    pub fn discover(client: &Client, changes: &Changes) -> Self {
        let (probing, starting) = (client.clone(), client.clone());
        let started = changes.clone();
        Self(Optional::discover(
            "CustomResourceDefinitions",
            &changes.sender,
            move || listable(probing.clone()),
            move |()| reflect_cut(Api::all(starting), started, trim::definition),
        ))
    }

    /// Resolves once the probe has finished or is retrying. When the
    /// probe succeeded, the store has its first full list by then,
    /// unless that list took longer than `ATTEMPT` in `discovery`.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every CRD, or none when the store is not running.
    pub fn state(&self) -> Vec<Arc<CustomResourceDefinition>> {
        match self.0.state() {
            State::On(store) => store.state(),
            State::Probing | State::Retrying | State::Off(_) => Vec::new(),
        }
    }
}

/// Succeeds when a list of CRDs, limited to one object, succeeds. Every
/// API server serves the kind, so the probe checks only that
/// `transect` may list it.
async fn listable(client: Client) -> Result<(), Refusal> {
    let crds: Api<CustomResourceDefinition> = Api::all(client);
    let one = ListParams::default().limit(1);
    crds.list_metadata(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "Listing CustomResourceDefinitions did not complete");
    })?;
    Ok(())
}
