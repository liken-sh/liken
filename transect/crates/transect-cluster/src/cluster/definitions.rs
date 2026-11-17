//! A watch on CustomResourceDefinitions, each cut to the parts that
//! describe its kind, when `transect` may list them.
//!
//! Discovery runs in the background, as it does for attachments, so an
//! identity that may not list CRDs still gets its map.  Its custom
//! kinds' cards then use their images' descriptions instead.

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
    /// Starts finding whether `transect` may list CRDs, and a watch once
    /// it may.
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

    /// Resolves once discovery has first answered, and the store, when
    /// it may list CRDs, holds its first full list.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every CRD as of now, or none while the list is refused.
    pub fn state(&self) -> Vec<Arc<CustomResourceDefinition>> {
        match self.0.state() {
            State::On(store) => store.state(),
            State::Probing | State::Retrying | State::Off(_) => Vec::new(),
        }
    }
}

/// Whether a list of one CRD succeeds.  Every API server serves them.
async fn listable(client: Client) -> Result<(), Refusal> {
    let crds: Api<CustomResourceDefinition> = Api::all(client);
    let one = ListParams::default().limit(1);
    crds.list_metadata(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "cannot list CustomResourceDefinitions");
    })?;
    Ok(())
}
