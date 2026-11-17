use k8s_openapi::api::rbac::v1::{ClusterRole, ClusterRoleBinding};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use rstest::rstest;
use serde::de::DeserializeOwned;

use super::*;
use crate::ownership::Entry;
use crate::ownership::fixtures::meta;
use transect_resources::knowledge::fixtures::knowledge;

fn yaml<K: DeserializeOwned>(text: &str) -> K {
    serde_yaml::from_str(text).unwrap()
}

/// A cluster of one custom resource, a `Cache` in `shop` that owns a
/// `StatefulSet`, and the workloads in `cache-system` that may run its
/// controller.
struct World {
    pods: Vec<Pod>,
    /// The workloads' metadata, by group and kind, for the ownership walk.
    owners: Vec<(&'static str, &'static str, ObjectMeta)>,
    /// The ServiceAccounts granted the right to write caches' status.
    accounts: Vec<(String, String)>,
    managers: Vec<String>,
    /// The image the kind's knowledge gives for its controller.
    image: Option<String>,
    /// Caches outside any namespace, which run no pods.
    cluster_scoped: Vec<Object>,
}

impl World {
    fn new() -> Self {
        let world = Self {
            pods: Vec::new(),
            owners: vec![
                (
                    "cache.example.io",
                    "Cache",
                    meta(Some("shop"), "sessions", &[]),
                ),
                (
                    "apps",
                    "StatefulSet",
                    meta(
                        Some("shop"),
                        "sessions",
                        &[("cache.example.io/v1", "Cache", "sessions")],
                    ),
                ),
            ],
            accounts: Vec::new(),
            managers: Vec::new(),
            image: None,
            cluster_scoped: Vec::new(),
        };
        world.pod(
            "shop",
            "sessions-0",
            "StatefulSet",
            "sessions",
            "sessions",
            "redis",
            "Running",
        )
    }

    /// Adds a pod in `namespace` owned by the `kind` `owner`, running
    /// as `account` with one container.
    #[allow(clippy::too_many_arguments)]
    fn pod(
        mut self,
        namespace: &str,
        name: &str,
        kind: &str,
        owner: &str,
        account: &str,
        container: &str,
        phase: &str,
    ) -> Self {
        self.pods.push(yaml(&format!(
            "{{metadata: {{namespace: {namespace}, name: {name}, ownerReferences: [{{apiVersion: apps/v1, kind: {kind}, name: {owner}, uid: x, controller: true}}]}}, spec: {{serviceAccountName: {account}, containers: [{{name: {container}, image: 'ghcr.io/example/{container}:1.0'}}]}}, status: {{phase: {phase}}}}}"
        )));
        self
    }

    /// Adds a `Deployment` in `cache-system` with one running pod.
    fn deployment(mut self, name: &'static str, account: &str, container: &str) -> Self {
        self.owners
            .push(("apps", "Deployment", meta(Some("cache-system"), name, &[])));
        let pod = format!("{name}-1");
        self.pod(
            "cache-system",
            &pod,
            "Deployment",
            name,
            account,
            container,
            "Running",
        )
    }

    /// Grants `<namespace>/<account>` the right to write caches' status.
    fn grant(mut self, namespace: &str, account: &str) -> Self {
        self.accounts.push((namespace.into(), account.into()));
        self
    }

    /// Gives the kind knowledge that names `image` as its controller's.
    fn knowing(mut self, image: &str) -> Self {
        self.image = Some(image.into());
        self
    }

    /// Adds a `Cache` outside any namespace that runs no pods.
    fn cluster_scoped(mut self, name: &str) -> Self {
        let cache = Object::new("cache.example.io", "Cache", None, name);
        self.cluster_scoped.push(cache);
        self
    }

    fn manager(mut self, manager: &str) -> Self {
        self.managers.push(manager.into());
        self
    }

    /// The controlled-by edges, as each end's name, whether it is a
    /// guess, and whether kind knowledge gave it.
    fn edges(&self) -> Vec<(String, String, bool, bool)> {
        let role: ClusterRole = yaml(
            "{metadata: {name: cache-operator}, rules: [{apiGroups: [cache.example.io], resources: [caches/status], verbs: [update]}]}",
        );
        let subjects: Vec<String> = self
            .accounts
            .iter()
            .map(|(namespace, account)| {
                format!("{{kind: ServiceAccount, namespace: {namespace}, name: {account}}}")
            })
            .collect();
        let binding: ClusterRoleBinding = yaml(&format!(
            "{{metadata: {{name: cache-operator}}, roleRef: {{apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cache-operator}}, subjects: [{}]}}",
            subjects.join(", ")
        ));
        let grants = Grants::build(Rbac {
            cluster_roles: vec![&role],
            roles: vec![],
            cluster_role_bindings: vec![&binding],
            role_bindings: vec![],
            missing: vec![],
        });
        let pod_entries = self.pods.iter().map(|pod| Entry {
            group: "",
            kind: "Pod",
            meta: &pod.metadata,
        });
        let owner_entries =
            self.owners
                .iter()
                .map(|(group, kind, meta)| Entry { group, kind, meta });
        let ownership = Ownership::build(pod_entries.chain(owner_entries));
        let sessions = Object::new("cache.example.io", "Cache", Some("shop"), "sessions");
        let managers = StatusManagers::from([(sessions, self.managers.clone())]);
        let plurals =
            Plurals::from([(("cache.example.io".into(), "Cache".into()), "caches".into())]);
        let pods: Vec<&Pod> = self.pods.iter().collect();
        let name = |end: Endpoint| match end {
            Endpoint::Object(object) => object.name,
            other => format!("{other:?}"),
        };
        let knowledge = self.image.iter().map(|image| {
            knowledge(
                "caches.cache.example.io",
                &format!("{{controller: {{image: {image}}}}}"),
            )
        });
        let kinds = Kinds::resolve(&knowledge.collect::<Vec<_>>(), &plurals, &[]);
        let outside = &self.cluster_scoped;
        controlled_by(
            &grants, &managers, &plurals, &pods, &ownership, &kinds, outside,
        )
        .into_iter()
        .map(|edge| {
            let known = matches!(edge.provenance, Provenance::Kinds { .. });
            (name(edge.from), name(edge.to), edge.guessed, known)
        })
        .collect()
    }
}

fn edge(to: &str, guessed: bool) -> (String, String, bool, bool) {
    ("sessions".into(), to.into(), guessed, false)
}

/// An edge kind knowledge gave, which is never a guess.
fn known(to: &str) -> (String, String, bool, bool) {
    ("sessions".into(), to.into(), false, true)
}

/// An edge from a resource other than the namespaced `Cache`.
fn edge_from(from: &str, to: &str) -> (String, String, bool, bool) {
    (from.into(), to.into(), false, false)
}

#[rstest]
#[case::a_cluster_scoped_resource_that_runs_nothing(
    World::new()
        .deployment("operator", "operator", "manager")
        .grant("cache-system", "operator")
        .cluster_scoped("shared"),
    vec![edge_from("shared", "operator"), edge("operator", false)],
)]
#[case::one_candidate(
    World::new().deployment("operator", "operator", "manager").grant("cache-system", "operator"),
    vec![edge("operator", false)],
)]
#[case::narrowed_by_the_manager(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("webhook", "operator", "webhook")
        .grant("cache-system", "operator")
        .manager("manager"),
    vec![edge("operator", false)],
)]
#[case::a_generic_manager_leaves_a_guess(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("webhook", "operator", "webhook")
        .grant("cache-system", "operator")
        .manager("Go-http-client"),
    vec![edge("operator", true), edge("webhook", true)],
)]
#[case::two_accounts(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("backup", "backup", "backup")
        .grant("cache-system", "operator")
        .grant("cache-system", "backup"),
    vec![edge("backup", true), edge("operator", true)],
)]
#[case::named_by_its_image(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("webhook", "operator", "webhook")
        .grant("cache-system", "operator")
        .manager("Go-http-client")
        .knowing("example/manager"),
    vec![known("operator")],
)]
#[case::an_image_no_candidate_runs(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("webhook", "operator", "webhook")
        .grant("cache-system", "operator")
        .manager("Go-http-client")
        .knowing("example/other"),
    vec![edge("operator", true), edge("webhook", true)],
)]
#[case::an_image_with_no_grant(
    World::new()
        .deployment("operator", "operator", "manager")
        .deployment("webhook", "webhook", "webhook")
        .knowing("ghcr.io/example/manager"),
    vec![known("operator")],
)]
#[case::an_image_nothing_runs_with_no_grant(
    World::new().deployment("operator", "operator", "manager").knowing("example/other"),
    vec![],
)]
#[case::no_grant(World::new().deployment("operator", "operator", "manager"), vec![])]
#[case::an_account_that_runs_nothing(World::new().grant("cache-system", "operator"), vec![])]
#[case::a_finished_pod(
    World::new()
        .pod("cache-system", "setup-1", "Deployment", "setup", "operator", "setup", "Succeeded")
        .grant("cache-system", "operator"),
    vec![],
)]
#[case::not_the_resource_itself(
    World::new()
        .pod("shop", "sessions-1", "StatefulSet", "sessions", "writer", "redis", "Running")
        .grant("shop", "writer"),
    vec![],
)]
fn finds_the_controller(#[case] world: World, #[case] expected: Vec<(String, String, bool, bool)>) {
    assert_eq!(world.edges(), expected);
}
