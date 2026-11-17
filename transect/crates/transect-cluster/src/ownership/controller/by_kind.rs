//! Attribution by kind: one controller for every object of a custom
//! kind that attribution by object left without one.
//!
//! Attribution by object follows a custom workload's own pods and RBAC.
//! Most custom resources run no pods, such as a certificate or a scan
//! report, so the kind is attributed whole, by the first of two rules
//! that finds a workload:
//!
//! - the image the kind's knowledge gives for its controller, among the
//!   workloads that run pods;
//! - the `app.kubernetes.io/` labels its CRD shares with one workload
//!   (`packaged`), which is drawn as a guess.

use std::collections::HashSet;

use k8s_openapi::api::core::v1::Pod;

use super::managers::running;
use super::packaged::{Package, Packages, matched};
use super::{everyone, workloads_by_account};
use crate::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};
use transect_resources::knowledge::Kinds;

/// What attribution by kind reads.
pub struct Sources<'a> {
    /// Each custom kind, by group and kind, with its objects.
    pub custom: &'a [(String, String, Vec<Object>)],
    pub packages: &'a Packages,
    /// Each workload with the package its labels and annotations name.
    pub workloads: &'a [(Object, Package)],
    pub pods: &'a [&'a Pod],
    pub ownership: &'a Ownership,
    pub kinds: &'a Kinds,
}

/// A controlled-by edge from each object of `sources.custom` outside
/// `attributed` to the workload its kind's rule finds.
pub fn controlled_by_kind(sources: &Sources, attributed: &HashSet<&Object>) -> Vec<Edge> {
    let by_account = workloads_by_account(sources.pods, sources.ownership);
    let mut edges = Vec::new();
    for (group, kind, objects) in sources.custom {
        let left: Vec<&Object> = objects.iter().filter(|o| !attributed.contains(o)).collect();
        let Some(first) = left.first() else {
            continue;
        };
        let by_image = sources.kinds.of(group, kind).and_then(|knowledge| {
            let image = knowledge.controller_image.as_deref()?;
            let pool = everyone(&by_account, first);
            let pool: Vec<_> = pool
                .into_iter()
                .filter(|c| !(&c.workload.group == group && &c.workload.kind == kind))
                .collect();
            let found = running(&pool, image)?;
            let workloads = found.into_iter().map(|c| c.workload).collect();
            Some((workloads, knowledge.provenance("controller.image")))
        });
        let by_package = || {
            let (crd, package) = sources.packages.of(group, kind)?;
            let workloads = sources.workloads.iter().map(|(o, p)| (o, p));
            let (workload, labels) = matched(package, workloads)?;
            let provenance = Provenance::Package {
                crd: crd.clone(),
                labels,
            };
            Some((vec![workload], provenance))
        };
        let Some((workloads, provenance)) = by_image.or_else(by_package) else {
            continue;
        };
        // Several workloads that run the image are a guess, and so is
        // any match by labels, which show only what was installed together.
        let guessed = workloads.len() > 1 || matches!(provenance, Provenance::Package { .. });
        for object in left {
            edges.extend(workloads.iter().map(|workload| Edge {
                kind: Kind::ControlledBy,
                from: Endpoint::Object(object.clone()),
                to: Endpoint::Object(workload.clone()),
                guessed,
                provenance: provenance.clone(),
            }));
        }
    }
    edges
}

#[cfg(test)]
mod tests {
    use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
    use rstest::rstest;

    use super::*;
    use crate::ownership::Entry;
    use crate::ownership::fixtures::meta;
    use transect_graph::edges::Plurals;
    use transect_resources::knowledge::fixtures::knowledge;

    const GROUP: &str = "scans.example.io";

    fn report(name: &str) -> Object {
        Object::new(GROUP, "Report", Some("shop"), name)
    }

    /// A pod of the `Deployment` `scans/scanner`, which runs the image
    /// `example/scanner`.
    fn pod() -> Pod {
        serde_yaml::from_str(
            "{metadata: {namespace: scans, name: scanner-1, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: scanner, uid: x, controller: true}]}, spec: {serviceAccountName: scanner, containers: [{name: scanner, image: 'ghcr.io/example/scanner:1.0'}]}, status: {phase: Running}}",
        )
        .unwrap()
    }

    fn crd(annotations: &str) -> CustomResourceDefinition {
        serde_yaml::from_str(&format!(
            "metadata: {{name: reports.scans.example.io, annotations: {annotations}}}\nspec: {{group: {GROUP}, scope: Namespaced, names: {{kind: Report, plural: reports}}, versions: []}}"
        ))
        .unwrap()
    }

    const INSTANCE: &str = "{app.kubernetes.io/instance: scanner}";

    /// The edges attribution by kind draws for two Reports, the first
    /// already attributed, as each end's name, the rule's provenance,
    /// and whether the edge is a guess.
    fn edges(
        image: Option<&str>,
        crd_annotations: &str,
    ) -> Vec<(String, String, Provenance, bool)> {
        let pod = pod();
        let deployment = meta(Some("scans"), "scanner", &[]);
        let ownership = Ownership::build([
            Entry {
                group: "",
                kind: "Pod",
                meta: &pod.metadata,
            },
            Entry {
                group: "apps",
                kind: "Deployment",
                meta: &deployment,
            },
        ]);
        let plurals = Plurals::from([((GROUP.into(), "Report".into()), "reports".into())]);
        let known = image.map(|image| {
            knowledge(
                "reports.scans.example.io",
                &format!("{{controller: {{image: {image}}}}}"),
            )
        });
        let kinds = Kinds::resolve(&known.into_iter().collect::<Vec<_>>(), &plurals, &[]);
        let crd = crd(crd_annotations);
        let packages = Packages::read([&crd]);
        let scanner = Object::new("apps", "Deployment", Some("scans"), "scanner");
        let workloads = [(scanner, crate::ownership::package(&yaml_meta(INSTANCE)))];
        let custom = [(
            GROUP.to_string(),
            "Report".to_string(),
            vec![report("first"), report("second")],
        )];
        let first = report("first");
        let attributed = HashSet::from([&first]);
        let sources = Sources {
            custom: &custom,
            packages: &packages,
            workloads: &workloads,
            pods: &[&pod],
            ownership: &ownership,
            kinds: &kinds,
        };
        let name = |end: Endpoint| match end {
            Endpoint::Object(object) => object.name,
            other => format!("{other:?}"),
        };
        controlled_by_kind(&sources, &attributed)
            .into_iter()
            .map(|edge| {
                (
                    name(edge.from),
                    name(edge.to),
                    edge.provenance,
                    edge.guessed,
                )
            })
            .collect()
    }

    fn yaml_meta(annotations: &str) -> k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta {
        serde_yaml::from_str(&format!("{{annotations: {annotations}}}")).unwrap()
    }

    fn by_image() -> Provenance {
        Provenance::Kinds {
            knowledge: "reports.scans.example.io".into(),
            field: "controller.image".into(),
        }
    }

    fn by_package() -> Provenance {
        Provenance::Package {
            crd: "reports.scans.example.io".into(),
            labels: vec!["app.kubernetes.io/instance=scanner".into()],
        }
    }

    #[rstest]
    #[case::the_image_its_knowledge_names(Some("example/scanner"), "{}", Some((by_image(), false)))]
    #[case::the_image_before_the_package(Some("example/scanner"), INSTANCE, Some((by_image(), false)))]
    #[case::the_package_its_crd_names_as_a_guess(None, INSTANCE, Some((by_package(), true)))]
    #[case::an_image_nothing_runs(Some("example/other"), INSTANCE, Some((by_package(), true)))]
    #[case::neither(None, "{}", None)]
    fn attributes_each_object_left_by_its_kinds_rule(
        #[case] image: Option<&str>,
        #[case] annotations: &str,
        #[case] expected: Option<(Provenance, bool)>,
    ) {
        let expected: Vec<_> = expected
            .into_iter()
            .map(|(p, guessed)| ("second".to_string(), "scanner".to_string(), p, guessed))
            .collect();
        assert_eq!(edges(image, annotations), expected);
    }
}
