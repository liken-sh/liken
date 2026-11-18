//! Attributes a controller to each object of a custom kind that the
//! parent module left without one, such as a certificate or a scan
//! report. Every object of the kind gets the same controller, from the
//! first of these rules that finds a workload:
//!
//! - the workloads that run the controller image that the kind's
//!   knowledge gives,
//! - the workload that shares `app.kubernetes.io/` labels with the
//!   kind's CRD (see `packaged`), which is drawn as a guess.

use std::collections::HashSet;

use k8s_openapi::api::core::v1::Pod;

use super::managers::running;
use super::packaged::{Package, Packages, matched};
use super::{other_workloads, workloads_by_account};
use crate::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};
use transect_resources::knowledge::Kinds;

/// The cluster state that `controlled_by_kind` reads.
pub struct Sources<'a> {
    /// Each custom kind, by group and kind, with its objects.
    pub custom: &'a [(String, String, Vec<Object>)],
    pub packages: &'a Packages,
    /// Each workload with the package keys of its labels and annotations.
    pub workloads: &'a [(Object, Package)],
    pub pods: &'a [&'a Pod],
    pub ownership: &'a Ownership,
    pub kinds: &'a Kinds,
}

/// Returns a controlled-by edge from each object of `sources.custom`
/// that is not in `attributed` to the workloads that its kind's rule
/// finds.
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
            let pool = other_workloads(&by_account, first);
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
        // A match by labels only shows that the CRD and the workload were
        // installed together, so it is always a guess. Several workloads
        // that run the image are also a guess.
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

    /// The edges drawn for two `Report`s, the first already attributed,
    /// as the name at each end, the provenance, and whether the edge is
    /// a guess.
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
    #[case::the_image_its_knowledge_gives(Some("example/scanner"), "{}", Some((by_image(), false)))]
    #[case::the_image_before_the_package(Some("example/scanner"), INSTANCE, Some((by_image(), false)))]
    #[case::the_package_its_crd_names_as_a_guess(None, INSTANCE, Some((by_package(), true)))]
    #[case::an_image_nothing_runs(Some("example/other"), INSTANCE, Some((by_package(), true)))]
    #[case::neither(None, "{}", None)]
    fn attributes_each_unattributed_object_by_its_kinds_rule(
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
