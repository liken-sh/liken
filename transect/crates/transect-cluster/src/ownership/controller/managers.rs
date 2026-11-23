//! Narrows the candidate controllers of a kind by the field manager
//! that wrote the status of its objects.
//!
//! The API server records in `metadata.managedFields` which field
//! manager last wrote each part of an object. The client chooses the
//! manager name, and most controllers use the name of their binary, such
//! as `manager` or `prometheus-operator`. That name is often also the
//! name of a container, an image, or a command. A Go client that sets no
//! name is recorded as `Go-http-client`, which matches no candidate, so
//! the candidates from RBAC stay as they are.

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::Pod;

use transect_graph::edges::Object;

/// A workload whose pods run as a `ServiceAccount` that RBAC lets write
/// a kind's status, with the field manager names that would point to it.
#[derive(Clone, Debug, PartialEq)]
pub struct Candidate {
    pub workload: Object,
    names: BTreeSet<String>,
    /// The images of its pods' containers, without registry host or tag.
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

    /// Whether a container of its pods runs an image of the repository
    /// `wanted`. Registry host and tag are ignored, and the image may
    /// have more path segments before `wanted`, so an image pulled
    /// through a mirror with its own host and path prefix still matches.
    pub fn runs(&self, wanted: &str) -> bool {
        self.repositories
            .iter()
            .any(|repository| same_repository(repository, wanted))
    }

    /// Adds the manager names that would point to `pod`: its
    /// `ServiceAccount`, and for each container its name, the last path
    /// segment of its image, and the file name of its command.
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
                self.names.insert(normal(image_name(image)));
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

/// Returns the candidates with a name that matches one of `managers`.
/// When none matches, the managers say nothing about the candidates, so
/// it returns all of them.
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

/// Returns the candidates that run `image`, or `None` when no candidate
/// does.
pub fn running(candidates: &[Candidate], image: &str) -> Option<Vec<Candidate>> {
    let matching: Vec<Candidate> = candidates
        .iter()
        .filter(|candidate| candidate.runs(image))
        .cloned()
        .collect();
    (!matching.is_empty()).then_some(matching)
}

/// Whether `image` is of the repository `wanted`, compared as in
/// `Candidate::runs`.
pub fn runs_image(image: &str, wanted: &str) -> bool {
    same_repository(without_host(without_tag(image)), wanted)
}

fn same_repository(repository: &str, wanted: &str) -> bool {
    let wanted = without_host(without_tag(wanted));
    repository == wanted || repository.ends_with(&format!("/{wanted}"))
}

/// Removes the tag and digest from an image reference, so
/// `ghcr.io/example/operator:1.2@sha256:...` becomes
/// `ghcr.io/example/operator`. A colon before the last slash is a
/// registry port.
fn without_tag(image: &str) -> &str {
    let image = image.split_once('@').map_or(image, |(image, _)| image);
    match image.rfind(':') {
        Some(colon) if !image[colon..].contains('/') => &image[..colon],
        _ => image,
    }
}

/// Removes the registry host from an image path, so
/// `ghcr.io/example/operator` becomes `example/operator`. The first
/// segment is a host when it has a dot or a colon, or is `localhost`,
/// which is the rule of Docker's image reference format.
fn without_host(image: &str) -> &str {
    match image.split_once('/') {
        Some((first, rest)) if first.contains(['.', ':']) || first == "localhost" => rest,
        _ => image,
    }
}

/// Removes the `@<suffix>` that some controllers add to their manager
/// name, as in `helm-controller@alpha`.
fn manager(name: &str) -> &str {
    name.split_once('@').map_or(name, |(name, _)| name)
}

/// The last path segment of an image without its tag or digest, so
/// `ghcr.io/example/operator:1.2@sha256:...` gives `operator`.
fn image_name(image: &str) -> &str {
    let image = image.split_once('@').map_or(image, |(image, _)| image);
    let last = basename(image);
    last.split_once(':').map_or(last, |(name, _)| name)
}

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

/// Lowercases `name` and drops every character that is not an ASCII
/// letter or digit, so `PrometheusOperator`, `prometheus-operator`, and
/// `prometheus_operator` compare as equal.
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
        let pod: Pod = serde_yaml_ng::from_str(&format!(
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

    fn cache_and_metrics() -> Vec<Candidate> {
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
    #[case::with_a_suffix("exporter@alpha", vec!["metrics"])]
    #[case::a_generic_client("Go-http-client", vec!["cache-operator", "metrics"])]
    fn narrows_by_the_status_manager(#[case] manager: &str, #[case] expected: Vec<&str>) {
        let narrowed = narrow(cache_and_metrics(), &[manager.into()]);
        assert_eq!(names(&narrowed), expected);
    }

    #[rstest]
    #[case::the_same_path("example/cache", vec!["cache-operator"])]
    #[case::with_the_registry_host("ghcr.io/example/cache", vec!["cache-operator"])]
    #[case::the_last_part_of_a_longer_path("metrics-operator", vec!["metrics"])]
    #[case::an_image_no_candidate_runs("example/other", vec![])]
    #[case::not_a_prefix_of_another_name("cache-operator", vec![])]
    fn keeps_the_candidates_that_run_an_image(#[case] image: &str, #[case] expected: Vec<&str>) {
        let found = running(&cache_and_metrics(), image).unwrap_or_default();
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
            names(&narrow(cache_and_metrics(), &[])),
            vec!["cache-operator", "metrics"]
        );
    }
}
