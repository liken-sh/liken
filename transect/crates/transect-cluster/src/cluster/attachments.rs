//! A watch on VolumeAttachments, when the cluster serves them and
//! `transect` may list them.
//!
//! An attachment names the machine a CSI driver attached a volume to,
//! and the attachment is what keeps a lost machine's pod from starting
//! elsewhere.  Discovery runs in the background, as it does for
//! devices, so `transect` still draws the map of a cluster that refuses
//! the list.

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
    /// Starts finding whether the cluster serves VolumeAttachments and
    /// `transect` may list them, and a watch once found.
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

    /// Resolves once discovery has first answered, and the store, when
    /// it found the kind, holds its first full list.
    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every attachment as of now, or none while the kind is off.
    pub fn state(&self) -> Vec<Arc<VolumeAttachment>> {
        match self.0.state() {
            State::On(store) => store.state(),
            State::Probing | State::Retrying | State::Off(_) => Vec::new(),
        }
    }
}

/// Whether the API serves VolumeAttachments at `v1`, and a list of one
/// succeeds.
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
        tracing::warn!(%error, "cannot list VolumeAttachments");
    })?;
    Ok(())
}
