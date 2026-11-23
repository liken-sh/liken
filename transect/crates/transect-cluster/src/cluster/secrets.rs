//! An optional metadata watch on `Secret`s, which reads the intent
//! annotations that people write on them. It runs only with
//! `--secret-annotations`.
//!
//! RBAC has no permission that allows reading a `Secret`'s metadata
//! without its data, so an identity that may list `Secret`s may also
//! read their contents. So by default `transect` reads a `Secret`'s
//! intent from the object that writes the `Secret`, and the flag is for
//! an identity that already has that access, such as a workstation's
//! admin kubeconfig. The watch requests metadata only, so the API
//! server sends no `Secret` data. It also drops every annotation but
//! `transect`'s own, because another annotation can contain the data,
//! as `kubectl.kubernetes.io/last-applied-configuration` does.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Secret;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::api::{ListParams, PartialObjectMeta};
use kube::runtime::reflector::Store;
use kube::{Api, Client};

use super::discovery::{Optional, Refusal, State};
use super::reflect::{Changes, reflect_cut};
use transect_resources::resources::field;

/// The `Secret` metadata store, which is empty unless the watch is on.
#[derive(Clone, Default)]
pub struct SecretStore(Option<Optional<Store<PartialObjectMeta<Secret>>>>);

impl SecretStore {
    /// Starts checking whether `transect` may list `Secret`s, and starts
    /// the metadata watch once it may.
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

    /// Resolves once the first list check has finished and, when the
    /// watch is on, the store has its first full list.
    pub async fn synced(&self) {
        if let Some(optional) = &self.0 {
            optional.settled().await;
        }
    }

    /// Every `Secret`'s metadata, or `None` when the watch is off or
    /// `transect` may not list `Secret`s.
    pub fn state(&self) -> Option<Vec<Arc<PartialObjectMeta<Secret>>>> {
        match self.0.as_ref()?.state() {
            State::On(store) => Some(store.state()),
            State::Probing | State::Retrying | State::Off(_) => None,
        }
    }
}

/// Succeeds when `transect` may list `Secret` metadata. An `Api` of
/// `PartialObjectMeta` requests metadata only, so this list receives no
/// `Secret` data.
async fn listable(client: Client) -> Result<(), Refusal> {
    let secrets: Api<PartialObjectMeta<Secret>> = Api::all(client);
    let one = ListParams::default().limit(1);
    secrets.list(&one).await.inspect_err(|error| {
        tracing::warn!(%error, "Listing Secrets did not succeed, so --secret-annotations shows no annotations");
    })?;
    Ok(())
}

fn trim(secret: &mut PartialObjectMeta<Secret>) {
    keep_ours(&mut secret.metadata);
}

/// Removes the managed fields and every annotation that is not one of
/// `transect`'s own from `meta`.
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
    fn removes_every_annotation_but_transects() {
        let mut meta: ObjectMeta = serde_yaml_ng::from_str(
            "{name: login, annotations: {transect.liken.sh/depends-on: db, kubectl.kubernetes.io/last-applied-configuration: '{\"data\": {}}', example.com/note: x}}",
        )
        .unwrap();
        keep_ours(&mut meta);
        let keys: Vec<&String> = meta.annotations.iter().flatten().map(|(k, _)| k).collect();
        assert_eq!(keys, vec!["transect.liken.sh/depends-on"]);
    }
}
