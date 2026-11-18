//! Reads the resource name (plural) of every kind the API serves from
//! API discovery. A URL names a kind by its plural, such as
//! `repositories` for `Repository`. The API's author chooses the
//! plural, so it is never derived from the kind.
//!
//! Discovery runs in the background at startup, with one request per
//! API group, and retries after a transient failure. It also records
//! each kind's resource at the version its group prefers, so a custom
//! kind watched at that version needs no discovery of its own. When one
//! group's request fails, such as for an aggregated API whose server is
//! down, only that group's kinds are missing. `Cluster::plurals` adds
//! the plural of each watched custom kind, so a kind installed after
//! discovery has one too.

use std::collections::HashMap;

use futures::future::join_all;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{APIResource, APIResourceList};
use kube::Client;
use kube::core::ApiResource;
use tokio::sync::watch;

use super::discovery::{Optional, Ready, Refusal, State};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::Plurals;

#[derive(Clone)]
pub struct PluralStore(Optional<Discovered>);

/// Each kind's resource at the version its group prefers, by group and
/// kind.
pub type Preferred = HashMap<(String, String), ApiResource>;

#[derive(Clone)]
struct Discovered(Plurals, ClusterScoped, Preferred);

impl Ready for Discovered {
    async fn ready(&self) {}
}

impl PluralStore {
    pub fn discover(client: &Client, changed: &watch::Sender<()>) -> Self {
        let client = client.clone();
        Self(Optional::discover(
            "API discovery",
            changed,
            move || read(client.clone()),
            |(plurals, scoped, preferred)| Discovered(plurals, scoped, preferred),
        ))
    }

    pub async fn synced(&self) {
        self.0.settled().await;
    }

    /// Every plural found, or an empty map before discovery finishes.
    pub fn plurals(&self) -> Plurals {
        match self.0.state() {
            State::On(Discovered(plurals, ..)) => plurals,
            _ => Plurals::default(),
        }
    }

    /// Every cluster-scoped kind found, or an empty set before
    /// discovery finishes.
    pub fn cluster_scoped(&self) -> ClusterScoped {
        match self.0.state() {
            State::On(Discovered(_, scoped, _)) => scoped,
            _ => ClusterScoped::default(),
        }
    }

    /// Each kind's resource at its group's preferred version, or an
    /// empty map before discovery finishes.
    pub fn preferred(&self) -> Preferred {
        match self.0.state() {
            State::On(Discovered(.., preferred)) => preferred,
            _ => Preferred::default(),
        }
    }
}

async fn read(client: Client) -> Result<(Plurals, ClusterScoped, Preferred), Refusal> {
    let groups = client.list_api_groups().await.map_err(Refusal::from)?;
    let versions = groups.groups.into_iter().filter_map(|group| {
        let preferred = group
            .preferred_version
            .or(group.versions.into_iter().next());
        preferred.map(|version| (group.name, version.group_version))
    });
    let lists = versions.map(|(group, version)| {
        let client = client.clone();
        async move {
            match client.list_api_group_resources(&version).await {
                Ok(list) => Some((group, list)),
                Err(error) => {
                    tracing::warn!(%version, %error, "Listing the resources of an API group did not succeed, so its kinds have no plurals");
                    None
                }
            }
        }
    });
    let mut lists: Vec<(String, APIResourceList)> =
        join_all(lists).await.into_iter().flatten().collect();
    let core = client
        .list_core_api_resources("v1")
        .await
        .map_err(Refusal::from)?;
    lists.push((String::new(), core));
    Ok((plurals(&lists), cluster_scoped(&lists), preferred(&lists)))
}

/// Each resource in `lists` with its group. Subresources, such as
/// `pods/log`, are skipped because they have their parent's kind.
fn resources(lists: &[(String, APIResourceList)]) -> impl Iterator<Item = (&String, &APIResource)> {
    let resources = lists
        .iter()
        .flat_map(|(group, list)| list.resources.iter().map(move |r| (group, r)));
    resources.filter(|(_, resource)| !resource.name.contains('/'))
}

/// The plural of each kind in `lists`, by group and kind.
fn plurals(lists: &[(String, APIResourceList)]) -> Plurals {
    resources(lists)
        .map(|(group, resource)| {
            (
                (group.clone(), resource.kind.clone()),
                resource.name.clone(),
            )
        })
        .collect()
}

/// Each kind in `lists` as a resource at the version of its list, which
/// is its group's preferred version.
fn preferred(lists: &[(String, APIResourceList)]) -> Preferred {
    let list_of = |group: &String| lists.iter().find(|(name, _)| name == group);
    resources(lists)
        .filter_map(|(group, resource)| {
            let (_, list) = list_of(group)?;
            let api_version = list.group_version.clone();
            let version = api_version.rsplit('/').next().unwrap_or_default().into();
            let found = ApiResource {
                group: group.clone(),
                version,
                api_version,
                kind: resource.kind.clone(),
                plural: resource.name.clone(),
            };
            Some(((group.clone(), resource.kind.clone()), found))
        })
        .collect()
}

/// The kinds in `lists` that are not namespaced, by group and kind.
fn cluster_scoped(lists: &[(String, APIResourceList)]) -> ClusterScoped {
    resources(lists)
        .filter(|(_, resource)| !resource.namespaced)
        .map(|(group, resource)| (group.clone(), resource.kind.clone()))
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn list(resources: &[(&str, &str)]) -> APIResourceList {
        listed("v1", resources)
    }

    fn listed(group_version: &str, resources: &[(&str, &str)]) -> APIResourceList {
        APIResourceList {
            group_version: group_version.into(),
            resources: resources
                .iter()
                .map(|(name, kind)| APIResource {
                    name: (*name).into(),
                    kind: (*kind).into(),
                    namespaced: *kind != "Node",
                    ..APIResource::default()
                })
                .collect(),
        }
    }

    #[rstest]
    #[case::a_custom_kind("source.example.io", "Repository", Some("repositories"))]
    #[case::a_core_kind("", "Pod", Some("pods"))]
    #[case::a_kind_no_group_serves("source.example.io", "Bucket", None)]
    fn reads_each_plural_from_discovery(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] plural: Option<&str>,
    ) {
        let lists = [
            (
                "source.example.io".to_owned(),
                list(&[
                    ("repositories", "Repository"),
                    ("repositories/status", "Repository"),
                ]),
            ),
            (String::new(), list(&[("pods", "Pod"), ("pods/log", "Pod")])),
        ];
        let found = plurals(&lists);
        let key = (group.to_owned(), kind.to_owned());
        assert_eq!(found.get(&key).map(String::as_str), plural);
    }

    #[rstest]
    #[case::a_namespaced_kind("Pod", false)]
    #[case::a_cluster_scoped_kind("Node", true)]
    fn reads_each_scope_from_discovery(#[case] kind: &str, #[case] scoped: bool) {
        let lists = [(String::new(), list(&[("pods", "Pod"), ("nodes", "Node")]))];
        let found = cluster_scoped(&lists);
        assert_eq!(found.contains(&(String::new(), kind.to_owned())), scoped);
    }

    #[rstest]
    #[case::a_custom_kind("source.example.io", "Repository", Some(("v1beta2", "source.example.io/v1beta2", "repositories")))]
    #[case::a_core_kind("", "Pod", Some(("v1", "v1", "pods")))]
    #[case::a_kind_no_group_serves("source.example.io", "Bucket", None)]
    fn records_each_kind_at_its_groups_preferred_version(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] expected: Option<(&str, &str, &str)>,
    ) {
        let lists = [
            (
                "source.example.io".to_owned(),
                listed(
                    "source.example.io/v1beta2",
                    &[("repositories", "Repository")],
                ),
            ),
            (String::new(), listed("v1", &[("pods", "Pod")])),
        ];
        let found = preferred(&lists);
        let resource = found.get(&(group.to_owned(), kind.to_owned()));
        let found = resource.map(|r| {
            (
                r.version.as_str(),
                r.api_version.as_str(),
                r.plural.as_str(),
            )
        });
        assert_eq!(found, expected);
    }
}
