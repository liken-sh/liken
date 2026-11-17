//! A watch on Secrets' metadata, for the intent annotations written on
//! them, only when the person who runs `transect` turns it on.
//!
//! Kubernetes has no permission for a `Secret`'s metadata alone: an
//! identity that may list Secrets may read their data.  So by default
//! `transect` reads a `Secret`'s intent from the object that writes it,
//! and watches Secrets only with `--secret-annotations`, for an
//! identity that already has that access, such as a workstation's
//! admin kubeconfig.  The watch asks for metadata only, so the API
//! server sends no `Secret`'s data, and it keeps only `transect`'s own
//! annotations, because another annotation can hold the data itself,
//! as the copy of the manifest that `kubectl apply` writes does.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Secret;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::api::{ListParams, PartialObjectMeta};
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Refusal, State};
use super::reflect::{Changes, reflect_cut};
use transect_resources::resources::field;

/// Secrets as metadata, when the watch is on.
#[derive(Clone, Default)]
pub struct SecretStore(Option<Optional<Store<PartialObjectMeta<Secret>>>>);

impl SecretStore {
    /// Starts finding whether `transect` may list Secrets, and a watch of
    /// their metadata once it may.
    pub fn discover(client: &Client, changes: &Changes) -> Self {
        let (probing, starting) = (client.clone(), client.clone());
        let started = changes.clone();
        Self(Some(Optional::discover(
            "Secrets",
            &changes.sender,
            move || listable(probing.clone()),
            move |()| reflect_cut(Api::all(starting), started, trim),
        )))
    }

    /// Resolves once discovery has first answered, and the store, when
    /// the watch is on, holds its first full list.
    pub async fn synced(&self) {
        if let Some(optional) = &self.0 {
            optional.settled().await;
        }
    }

    /// Every Secret's metadata as of now, or none when the watch is off
    /// or `transect` may not list Secrets.
    pub fn state(&self) -> Option<Vec<Arc<PartialObjectMeta<Secret>>>> {
        match self.0.as_ref()?.state() {
            State::On(store) => Some(store.state()),
            State::Probing | State::Retrying | State::Off(_) => None,
        }
    }
}

/// Whether a list of one `Secret`'s metadata succeeds.  An `Api` of
/// metadata asks the API server for metadata alone, so neither this
/// list nor the watch includes a `Secret`'s data.
async fn listable(client: Client) -> Result<(), Refusal> {
    let secrets: Api<PartialObjectMeta<Secret>> = Api::all(client);
    let one = ListParams::default().limit(1);
    secrets.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "cannot list Secrets; --secret-annotations reads none");
    })?;
    Ok(())
}

/// A `Secret`'s metadata cut to what `keep_ours` keeps.
fn trim(secret: &mut PartialObjectMeta<Secret>) {
    keep_ours(&mut secret.metadata);
}

/// `meta` with no managed fields and no annotation but `transect`'s own.
fn keep_ours(meta: &mut ObjectMeta) {
    meta.managed_fields = None;
    if let Some(annotations) = &mut meta.annotations {
        annotations.retain(|key, _| field(key).is_some());
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    fn keeps_only_transects_annotations() {
        let mut meta: ObjectMeta = serde_yaml::from_str(
            "{name: login, annotations: {transect.liken.sh/depends-on: db, kubectl.kubernetes.io/last-applied-configuration: '{\"data\": {}}', example.com/note: x}}",
        )
        .unwrap();
        keep_ours(&mut meta);
        let keys: Vec<&String> = meta.annotations.iter().flatten().map(|(k, _)| k).collect();
        assert_eq!(keys, vec!["transect.liken.sh/depends-on"]);
    }
}
