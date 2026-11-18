//! A watch on `VolumeAttachment`s, when the cluster serves them and
//! `transect` may list them.
//!
//! A `VolumeAttachment` records the machine that a CSI driver attached
//! a volume to. While it exists, a volume that attaches to one machine
//! at a time cannot attach elsewhere, so a pod of a lost machine that
//! uses the volume cannot start on another machine. A background probe
//! checks that the kind is served and listable, as it does for devices,
//! so `transect` still draws the map of a cluster that refuses the
//! list.

use std::sync::Arc;

use k8s_openapi::api::storage::v1::VolumeAttachment;
use kube::api::ListParams;
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Refusal, State};
use super::reflect::{Changes, reflect};

const VERSION: &str = "storage.k8s.io/v1";

#[derive(Clone)]
pub struct AttachmentStore(Optional<Store<VolumeAttachment>>);

impl AttachmentStore {
    /// Starts probing whether the cluster serves `VolumeAttachment`s
    /// and `transect` may list them, and starts a watch when the probe
    /// succeeds.
    pub fn discover(client: &Client, changes: &Changes) -> Self {
        let (probing, starting) = (client.clone(), client.clone());
        let started = changes.clone();
        Self(Optional::discover(
            "VolumeAttachments",
            &changes.sender,
            move || served(probing.clone()),
            move |()| reflect(Api::all(starting), started),
        ))
    }

    /// Resolves once the probe has finished or is retrying. When the
    /// probe succeeded, the store has its first full list by then,
    /// unless that list took longer than `ATTEMPT` in `discovery`.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every `VolumeAttachment`, or none when the store is not running.
    pub fn state(&self) -> Vec<Arc<VolumeAttachment>> {
        match self.0.state() {
            State::On(store) => store.state(),
            State::Probing | State::Retrying | State::Off(_) => Vec::new(),
        }
    }
}

/// Succeeds when the API server serves `VolumeAttachment`s at `v1` and
/// a list of them, limited to one object, succeeds.
async fn served(client: Client) -> Result<(), Refusal> {
    let resources = client.list_api_group_resources(VERSION).await?;
    if !resources
        .resources
        .iter()
        .any(|r| r.kind == "VolumeAttachment")
    {
        return Err(Refusal::NotServed);
    }
    let attachments: Api<VolumeAttachment> = Api::all(client);
    let one = ListParams::default().limit(1);
    attachments.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "Listing VolumeAttachments did not complete");
    })?;
    Ok(())
}
