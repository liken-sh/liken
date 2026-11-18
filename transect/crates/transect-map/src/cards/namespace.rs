//! The namespace card: the namespace's name and health, the hosts its
//! Ingresses serve, and a chip for each of its workloads.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use super::chips::{Chip, Sorted};
use transect_cluster::ownership::Managed;
use transect_cluster::workloads::Objects;
use transect_graph::edges::{Loop, Object};
use transect_ground::devices::Holds;
use transect_network::addresses::Depends;
use transect_network::policy::Calls;
use transect_observe::health::{Health, Level, Reason};
use transect_resources::intent::{Docs, Line, ObjectIntent};
use transect_resources::knowledge::Named;
use transect_supply::gitops::CodeSection;

#[derive(Debug, PartialEq)]
pub struct NamespaceCard {
    pub name: String,
    /// `why`, `gotcha`, `provides`, and `docs` come from the namespace's
    /// declared intent.
    pub why: Option<Line>,
    pub gotcha: Option<Line>,
    pub provides: Option<Line>,
    pub docs: Option<Docs>,
    /// The same health as the namespace's rectangle on the cluster page,
    /// so the card's reasons are the sentences that the "Now" section
    /// shows.
    pub health: Health,
    /// The page that each of the health's reasons links to, in the same
    /// order, or none for a reason about the namespace itself.
    pub reason_pages: Vec<Option<String>>,
    /// Every host that the namespace's Ingresses route, each once, in
    /// DNS order.
    pub hosts: Vec<String>,
    pub chips: Vec<Chip>,
    /// How many PersistentVolumeClaims the namespace has.
    pub volumes: usize,
    /// What the namespace's workloads depend on outside the namespace,
    /// and what outside the namespace depends on them.
    pub depends: Depends,
    /// Where the namespace is declared in git, or why the card cannot
    /// show it.
    pub code: CodeSection,
    /// The controllers of its custom resources, and what the
    /// namespace's own controllers manage.
    pub managed: Managed,
    /// The loops that the namespace is in.
    pub loops: Vec<Loop>,
    /// The other namespaces that its network policies allow to call
    /// it.
    pub calls: Calls,
    /// The devices that its workloads hold.
    pub holds: Holds,
    pub layer_named: Option<Named>,
    /// The sentences for log patterns that matched in this namespace
    /// and are informational, for the "Worth knowing" lines. Patterns at
    /// error or warning level are in the health's reasons instead.
    pub logged: Vec<Reason>,
}

impl NamespaceCard {
    /// The card for `namespace`, with a chip for each of `workloads`,
    /// the namespace's top-level owners. A chip is colored by the
    /// workload's health in `by_workload`, links to the page that `href`
    /// returns, and shows the replica count from `objects`.
    #[allow(clippy::too_many_arguments)]
    pub fn build<'a>(
        namespace: &str,
        health: Health,
        objects: &Objects,
        ingresses: &[Arc<Ingress>],
        workloads: impl IntoIterator<Item = &'a Object>,
        by_workload: &HashMap<Object, Health>,
        volumes: usize,
        href: impl Fn(&Object) -> Option<String>,
    ) -> Self {
        let here =
            |object_namespace: Option<String>| object_namespace.as_deref() == Some(namespace);
        let mut replicas: HashMap<(&str, String), i32> = HashMap::new();
        for deployment in objects.deployments.iter().filter(|o| here(o.namespace())) {
            let desired = deployment.spec.replicas.unwrap_or(1);
            replicas.insert(("Deployment", deployment.name_any()), desired);
        }
        for stateful_set in objects.stateful_sets.iter().filter(|o| here(o.namespace())) {
            let desired = stateful_set.spec.replicas.unwrap_or(1);
            replicas.insert(("StatefulSet", stateful_set.name_any()), desired);
        }
        for daemon_set in objects.daemon_sets.iter().filter(|o| here(o.namespace())) {
            let scheduled = daemon_set
                .status
                .as_ref()
                .map(|s| s.desired_number_scheduled);
            replicas.insert(("DaemonSet", daemon_set.name_any()), scheduled.unwrap_or(0));
        }
        let mut sorted = Sorted::default();
        for workload in workloads {
            let count = replicas.get(&(workload.kind.as_str(), workload.name.clone()));
            let level = by_workload.get(workload).map(|health| health.level);
            sorted.add(
                workload,
                count.copied(),
                level.unwrap_or_default(),
                href(workload),
            );
        }
        let ingresses = ingresses.iter().filter(|o| here(o.namespace()));
        Self {
            name: namespace.into(),
            why: None,
            gotcha: None,
            provides: None,
            docs: None,
            health,
            reason_pages: Vec::new(),
            hosts: hosts(ingresses.map(AsRef::as_ref)),
            chips: sorted.chips(),
            volumes,
            depends: Depends::default(),
            code: CodeSection::default(),
            managed: Default::default(),
            loops: Vec::new(),
            calls: Calls::default(),
            holds: Holds::default(),
            layer_named: None,
            logged: Vec::new(),
        }
    }

    /// Links each reason to the page that `page` returns for it.
    pub fn with_reason_pages(self, page: impl Fn(&Reason) -> Option<String>) -> Self {
        let reason_pages = self.health.reasons.iter().map(page).collect();
        Self {
            reason_pages,
            ..self
        }
    }

    /// Each of the health's reasons with the page it links to, if any.
    pub fn reason_lines(&self) -> Vec<(&Reason, Option<&str>)> {
        let pages = self.reason_pages.iter().map(Option::as_deref);
        self.health
            .reasons
            .iter()
            .zip(pages.chain(std::iter::repeat(None)))
            .collect()
    }

    pub fn with_logged(self, logged: Vec<Reason>) -> Self {
        Self { logged, ..self }
    }

    /// Sets the layer that the `KindKnowledge` of the namespace's kinds
    /// names. The map uses it to place the namespace when neither an annotation
    /// nor a selector does.
    pub fn with_layer_named(self, layer_named: Option<Named>) -> Self {
        Self {
            layer_named,
            ..self
        }
    }

    /// Sets the namespace's intent, and titles each chip with the `why`
    /// that `whys` has for its workload name.
    pub fn with_intent(
        self,
        intent: Option<&ObjectIntent>,
        whys: &HashMap<String, String>,
    ) -> Self {
        let intent = intent.cloned().unwrap_or_default();
        Self {
            why: intent.why,
            gotcha: intent.gotcha,
            provides: intent.provides,
            docs: intent.docs,
            chips: self
                .chips
                .into_iter()
                .map(|chip| chip.titled(whys))
                .collect(),
            ..self
        }
    }

    /// The legend that explains the card's marked lines, when any line
    /// is marked.
    pub fn legend(&self) -> Option<String> {
        let lines = [
            self.why.as_ref().map(|line| &line.provenance),
            self.gotcha.as_ref().map(|line| &line.provenance),
            self.provides.as_ref().map(|line| &line.provenance),
            self.docs.as_ref().map(|docs| &docs.provenance),
            self.layer_named.as_ref().map(|named| &named.provenance),
        ];
        transect_graph::edges::provenance::legend(lines.into_iter().flatten())
    }

    pub fn with_calls(self, calls: Calls) -> Self {
        Self { calls, ..self }
    }

    /// `depends`, `holds`, `managed`, and `code` come from the edge
    /// graph, so a view that builds no edge graph leaves them at their
    /// defaults.
    pub fn with_holds(self, holds: Holds) -> Self {
        Self { holds, ..self }
    }

    pub fn with_depends(self, depends: Depends) -> Self {
        Self { depends, ..self }
    }

    pub fn with_managed(self, managed: Managed) -> Self {
        Self { managed, ..self }
    }

    pub fn with_code(self, code: CodeSection) -> Self {
        Self { code, ..self }
    }

    /// Keeps the loops of `loops` that contain the card's namespace.
    pub fn with_loops(self, loops: &[Loop]) -> Self {
        let loops = loops.iter().filter(|found| found.holds(&self.name));
        Self {
            loops: loops.cloned().collect(),
            ..self
        }
    }

    /// The CSS class of the health dot, the same as the "Now" section
    /// uses.
    pub fn dot_class(&self) -> &'static str {
        match self.health.level {
            Level::Healthy => "dot-clear",
            Level::Notice => "concern-notice",
            Level::Warning => "concern-warning",
            Level::Failing => "concern-failing",
        }
    }

    /// The CSS class of the namespace's frame on the namespace page, so
    /// the frame shows the same health as the namespace's rectangle on
    /// the cluster page.
    pub fn frame_class(&self) -> &'static str {
        match self.health.level {
            Level::Healthy | Level::Notice => "opened",
            Level::Warning => "opened opened-warning",
            Level::Failing => "opened opened-failing",
        }
    }
}

/// The hosts of every rule in `ingresses`, each once, in DNS order. A
/// rule with no host matches any host, so it adds no host to the list.
fn hosts<'a>(ingresses: impl IntoIterator<Item = &'a Ingress>) -> Vec<String> {
    let hosts: BTreeSet<String> = ingresses
        .into_iter()
        .filter_map(|ingress| ingress.spec.as_ref()?.rules.as_ref())
        .flatten()
        .filter_map(|rule| rule.host.clone())
        .collect();
    let mut hosts: Vec<String> = hosts.into_iter().collect();
    hosts.sort_by(|a, b| transect_graph::dns::order(a, b));
    hosts
}

#[cfg(test)]
mod tests {
    use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
    use k8s_openapi::api::batch::v1::CronJob;
    use rstest::rstest;
    use serde::de::DeserializeOwned;

    use super::super::chips::Label;
    use super::super::chips::fixtures::*;
    use super::*;

    fn object<K: DeserializeOwned>(yaml: &str) -> Arc<K> {
        Arc::new(serde_yaml::from_str(yaml).unwrap())
    }

    fn deployment(namespace: &str, name: &str, replicas: i32) -> Arc<Deployment> {
        object(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{replicas: {replicas}, selector: {{}}, template: {{}}}}"
        ))
    }

    fn stateful_set(namespace: &str, name: &str, replicas: i32) -> Arc<StatefulSet> {
        object(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{replicas: {replicas}, selector: {{}}, template: {{}}}}"
        ))
    }

    fn daemon_set(namespace: &str, name: &str, scheduled: i32) -> Arc<DaemonSet> {
        object(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nstatus: {{desiredNumberScheduled: {scheduled}, currentNumberScheduled: 0, numberMisscheduled: 0, numberReady: 0}}"
        ))
    }

    fn cron_job(namespace: &str, name: &str) -> Arc<CronJob> {
        object(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}"
        ))
    }

    fn ingress(namespace: &str, hosts: &[&str]) -> Arc<Ingress> {
        let rules: Vec<String> = hosts
            .iter()
            .map(|host| format!("{{host: {host}}}"))
            .collect();
        object(&format!(
            "metadata: {{namespace: {namespace}, name: web}}\nspec: {{rules: [{}]}}",
            rules.join(", ")
        ))
    }

    fn workload(group: &str, kind: &str, name: &str) -> Object {
        Object::new(group, kind, Some("shop"), name)
    }

    #[rstest]
    fn reads_only_the_selected_namespaces_objects() {
        let deployments = [deployment("shop", "web", 3), deployment("blog", "web", 2)];
        let stateful_sets = [stateful_set("shop", "database", 1)];
        let daemon_sets = [daemon_set("shop", "agent", 4)];
        let cron_jobs = [cron_job("shop", "report"), cron_job("blog", "digest")];
        let objects = Objects {
            deployments: &deployments,
            stateful_sets: &stateful_sets,
            daemon_sets: &daemon_sets,
            cron_jobs: &cron_jobs,
            pods: &[],
        };
        let ingresses = [
            ingress("shop", &["shop.example.com", "api.shop.example.com"]),
            ingress("blog", &["blog.example.com"]),
        ];
        let workloads = [
            workload("apps", "Deployment", "web"),
            workload("apps", "StatefulSet", "database"),
            workload("apps", "DaemonSet", "agent"),
            workload("batch", "CronJob", "report"),
            workload("cache.example.io", "Cache", "sessions"),
        ];
        let failing = Health {
            level: Level::Failing,
            reasons: Vec::new(),
        };
        let by_workload =
            HashMap::from([(workload("cache.example.io", "Cache", "sessions"), failing)]);
        let card = NamespaceCard::build(
            "shop",
            Health::default(),
            &objects,
            &ingresses,
            &workloads,
            &by_workload,
            2,
            |_| None,
        );
        let sessions = Chip {
            level: Level::Failing,
            ..owned("Cache", "sessions", true)
        };
        assert_eq!(
            card,
            NamespaceCard {
                name: "shop".into(),
                why: None,
                gotcha: None,
                provides: None,
                docs: None,
                health: Health::default(),
                reason_pages: Vec::new(),
                hosts: vec!["shop.example.com".into(), "api.shop.example.com".into()],
                chips: vec![
                    replicated("agent", 4),
                    replicated("web", 3),
                    single(&["database"]),
                    chip(Label::CronJobs(1)),
                    sessions,
                ],
                volumes: 2,
                depends: Depends::default(),
                code: CodeSection::default(),
                managed: Default::default(),
                loops: Vec::new(),
                calls: Calls::default(),
                holds: Holds::default(),
                layer_named: None,
                logged: Vec::new(),
            }
        );
    }

    #[rstest]
    fn ignores_the_replicas_of_a_workload_in_another_namespace() {
        let deployments = [deployment("blog", "web", 2)];
        let objects = Objects {
            deployments: &deployments,
            stateful_sets: &[],
            daemon_sets: &[],
            cron_jobs: &[],
            pods: &[],
        };
        let workloads = [workload("apps", "Deployment", "web")];
        let card = NamespaceCard::build(
            "shop",
            Health::default(),
            &objects,
            &[],
            &workloads,
            &HashMap::new(),
            0,
            |_| None,
        );
        assert_eq!(card.chips, vec![owned("Deployment", "web", false)]);
    }

    #[rstest]
    #[case::one_host_twice(vec![vec!["shop.example.com"], vec!["shop.example.com"]], &["shop.example.com"])]
    #[case::no_rules(vec![vec![]], &[])]
    // `~` is YAML's null: a rule with no host, which matches any host.
    #[case::a_rule_with_no_host(vec![vec!["~", "shop.example.com"]], &["shop.example.com"])]
    #[case::sorted(vec![vec!["b.example.com", "a.example.com"]], &["a.example.com", "b.example.com"])]
    fn lists_each_host_once_in_order(#[case] ingresses: Vec<Vec<&str>>, #[case] expected: &[&str]) {
        let ingresses: Vec<Ingress> = ingresses
            .iter()
            .map(|hosts| Arc::unwrap_or_clone(ingress("shop", hosts)))
            .collect();
        assert_eq!(hosts(&ingresses), expected);
    }
}
