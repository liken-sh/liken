//! Narrowing a kind's candidate controllers by the field manager that
//! writes an object's status.
//!
//! The API server records, in `metadata.managedFields`, the manager that
//! last wrote each part of an object.  A client names itself, and most
//! controllers take the name of their binary, such as `manager` or
//! `prometheus-operator`, so the name often matches a container, its
//! image, or its command.  Many Go clients send no name and appear as
//! `Go-http-client`, which matches nothing; then the candidates stay as
//! RBAC found them, unless the kind's knowledge names the image its
//! controller runs.

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::Pod;

use transect_graph::edges::Object;

/// A workload whose pods run as a `ServiceAccount` that may write a
/// kind's status, with the names it could appear under as a field
/// manager.
#[derive(Clone, Debug, PartialEq)]
pub struct Candidate {
    pub workload: Object,
    names: BTreeSet<String>,
    /// The image repositories its pods run, without a registry host.
    repositories: BTreeSet<String>,
}

impl Candidate {
    pub fn new(workload: Object) -> Self {
        let names = BTreeSet::from([normal(&workload.name)]);
        Self {
            workload,
            names,
            repositories: BTreeSet::new(),
        }
    }

    /// Whether its pods run the image repository `wanted`, matched
    /// without the registry host on either side, so a mirror that
    /// serves the image under its own host, or under a path of its own
    /// before the repository, still matches.
    pub fn runs(&self, wanted: &str) -> bool {
        self.repositories
            .iter()
            .any(|repository| same_repository(repository, wanted))
    }

    /// Adds the names `pod` could write under: its `ServiceAccount`, and
    /// each container's name, image, and command.
    pub fn add_pod(&mut self, pod: &Pod) {
        let Some(spec) = &pod.spec else {
            return;
        };
        if let Some(account) = &spec.service_account_name {
            self.names.insert(normal(account));
        }
        for container in &spec.containers {
            self.names.insert(normal(&container.name));
            if let Some(image) = &container.image {
                self.names.insert(normal(repository(image)));
                let path = without_host(without_tag(image));
                self.repositories.insert(path.to_string());
            }
            let command = container.command.iter().flatten().next();
            if let Some(command) = command {
                self.names.insert(normal(basename(command)));
            }
        }
    }
}

/// The candidates whose names match one of `managers`, or every
/// candidate when none matches, because a manager that names no
/// candidate gives no evidence of which of them wrote.
pub fn narrow(candidates: Vec<Candidate>, managers: &[String]) -> Vec<Candidate> {
    let managers: BTreeSet<String> = managers.iter().map(|m| normal(manager(m))).collect();
    let matching: Vec<Candidate> = candidates
        .iter()
        .filter(|candidate| !candidate.names.is_disjoint(&managers))
        .cloned()
        .collect();
    if matching.is_empty() {
        candidates
    } else {
        matching
    }
}

/// The candidates that run `image`, or none when no candidate does,
/// because an image that names no candidate gives no evidence of which
/// of them is the controller.
pub fn running(candidates: &[Candidate], image: &str) -> Option<Vec<Candidate>> {
    let matching: Vec<Candidate> = candidates
        .iter()
        .filter(|candidate| candidate.runs(image))
        .cloned()
        .collect();
    (!matching.is_empty()).then_some(matching)
}

/// Whether `image` is of the repository `wanted`, both without the
/// registry host, so a mirror that serves the image under its own host,
/// or under a path of its own before the repository, still matches.
pub fn runs_image(image: &str, wanted: &str) -> bool {
    same_repository(without_host(without_tag(image)), wanted)
}

fn same_repository(repository: &str, wanted: &str) -> bool {
    let wanted = without_host(without_tag(wanted));
    repository == wanted || repository.ends_with(&format!("/{wanted}"))
}

/// An image without its tag or digest: `ghcr.io/example/operator` of
/// `ghcr.io/example/operator:1.2@sha256:...`.  A colon before the last
/// slash is a registry's port, not a tag.
fn without_tag(image: &str) -> &str {
    let image = image.split_once('@').map_or(image, |(image, _)| image);
    match image.rfind(':') {
        Some(colon) if !image[colon..].contains('/') => &image[..colon],
        _ => image,
    }
}

/// An image's path without its registry host: `example/operator` of
/// `ghcr.io/example/operator`.  The first part is a host when it has a
/// dot or a port, or is `localhost`, as the container runtimes read it.
fn without_host(image: &str) -> &str {
    match image.split_once('/') {
        Some((first, rest)) if first.contains(['.', ':']) || first == "localhost" => rest,
        _ => image,
    }
}

/// A manager's name without the `@<host>` some controllers add, such as
/// `helm-controller@alpha`.
fn manager(name: &str) -> &str {
    name.split_once('@').map_or(name, |(name, _)| name)
}

/// The last path segment of an image, without its tag or digest:
/// `operator` of `ghcr.io/example/operator:1.2@sha256:...`.
fn repository(image: &str) -> &str {
    let image = image.split_once('@').map_or(image, |(image, _)| image);
    let last = basename(image);
    last.split_once(':').map_or(last, |(name, _)| name)
}

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

/// `PrometheusOperator`, `prometheus-operator`, and
/// `prometheus_operator` all compare as one name.
fn normal(name: &str) -> String {
    name.chars()
        .filter(char::is_ascii_alphanumeric)
        .map(|c| c.to_ascii_lowercase())
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn candidate(name: &str, container: &str, image: &str, command: &str) -> Candidate {
        let pod: Pod = serde_yaml::from_str(&format!(
            "{{spec: {{serviceAccountName: {name}, containers: [{{name: {container}, image: '{image}', command: ['{command}']}}]}}}}"
        ))
        .unwrap();
        let mut candidate = Candidate::new(Object::new("apps", "Deployment", Some("ops"), name));
        candidate.add_pod(&pod);
        candidate
    }

    fn names(candidates: &[Candidate]) -> Vec<&str> {
        candidates
            .iter()
            .map(|candidate| candidate.workload.name.as_str())
            .collect()
    }

    fn two() -> Vec<Candidate> {
        vec![
            candidate(
                "cache-operator",
                "manager",
                "ghcr.io/example/cache:1.0",
                "/manager",
            ),
            candidate(
                "metrics",
                "exporter",
                "quay.io/example/metrics-operator:v2@sha256:abc",
                "/bin/run",
            ),
        ]
    }

    #[rstest]
    #[case::a_container_name("manager", vec!["cache-operator"])]
    #[case::an_image_in_another_case("MetricsOperator", vec!["metrics"])]
    #[case::a_command("run", vec!["metrics"])]
    #[case::a_workload_name("cache-operator", vec!["cache-operator"])]
    #[case::with_a_host("exporter@alpha", vec!["metrics"])]
    #[case::a_generic_client("Go-http-client", vec!["cache-operator", "metrics"])]
    fn narrows_by_the_status_manager(#[case] manager: &str, #[case] expected: Vec<&str>) {
        let narrowed = narrow(two(), &[manager.into()]);
        assert_eq!(names(&narrowed), expected);
    }

    #[rstest]
    #[case::the_same_path("example/cache", vec!["cache-operator"])]
    #[case::with_the_registry_host("ghcr.io/example/cache", vec!["cache-operator"])]
    #[case::the_last_part_of_a_longer_path("metrics-operator", vec!["metrics"])]
    #[case::an_image_no_candidate_runs("example/other", vec![])]
    #[case::not_a_prefix_of_another_name("cache-operator", vec![])]
    fn keeps_the_candidates_that_run_an_image(#[case] image: &str, #[case] expected: Vec<&str>) {
        let found = running(&two(), image).unwrap_or_default();
        assert_eq!(names(&found), expected);
    }

    #[rstest]
    fn a_mirror_serves_the_image_under_its_own_host() {
        let mirrored = candidate(
            "cache-operator",
            "manager",
            "mirror.local:5000/example/cache:1.0",
            "/manager",
        );
        assert!(mirrored.runs("example/cache"));
    }

    #[rstest]
    fn no_manager_keeps_every_candidate() {
        assert_eq!(
            names(&narrow(two(), &[])),
            vec!["cache-operator", "metrics"]
        );
    }
}
