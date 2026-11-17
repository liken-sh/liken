//! Where a running container's image came from, as its block on the pod
//! page and its workload's card show it: the commit it was built from,
//! its release, its description, and its documentation, or the reason
//! that transect cannot read them.

use k8s_openapi::api::core::v1::{ContainerStatus, Pod};

use super::{Failure, Known, Lookup, Provenance, parse};
use crate::forge::{self, Commit, Forges};
use transect_graph::pods::finished;

#[derive(Clone, Debug, PartialEq)]
pub enum Origin {
    /// The labels have not been read yet.
    Reading,
    Unreadable(Failure),
    Read(Built),
}

/// The facts from an image's labels, in the form that a page draws.
#[derive(Clone, Debug, PartialEq)]
pub struct Built {
    /// The commit, or none when the image has no source label.
    pub commit: Option<Commit>,
    /// The source label as given, which the card compares with the
    /// repository a GitOps tool applies from.
    pub source: Option<String>,
    pub version: Option<String>,
    pub description: Option<String>,
    pub documentation: Option<String>,
}

impl Built {
    fn of(provenance: &Provenance, forges: &Forges) -> Self {
        let commit = provenance.source.as_ref().map(|source| {
            let revision = provenance.revision.as_deref().unwrap_or_default();
            forge::commit(source, revision, forges)
        });
        Self {
            commit,
            source: provenance.source.clone(),
            version: provenance.version.clone(),
            description: provenance.description.clone(),
            documentation: provenance.documentation.clone(),
        }
    }
}

/// The labels that transect has read so far and the forges they link
/// to.  Every origin on a page is read from them.
#[derive(Clone, Copy)]
pub struct Origins<'a> {
    pub known: &'a Known,
    pub forges: &'a Forges,
}

impl Origins<'_> {
    /// The origin of the image that a container status of `pod` names.
    /// It is `None` when the status names no image that a registry
    /// serves, as before the pull, or when `pod` has finished.  transect
    /// reads only the images of pods that have not finished, so a
    /// finished pod's image would show as still being read for as long
    /// as the pod exists.
    pub fn of(&self, pod: &Pod, status: &ContainerStatus) -> Option<Origin> {
        if finished(pod) {
            return None;
        }
        let image = parse(&status.image_id)?;
        Some(match self.known.lookup(&image) {
            Lookup::Reading => Origin::Reading,
            Lookup::Failed(failure) => Origin::Unreadable(failure),
            Lookup::Read(provenance) => Origin::Read(Built::of(&provenance, self.forges)),
        })
    }

    /// The origins of each image `pods` run in their app containers, by
    /// the image the spec names, each once, in the order the pods list
    /// them.  A rollout runs two digests of one image, so one image can
    /// have two origins.
    pub fn of_pods<'p>(&self, pods: impl IntoIterator<Item = &'p Pod>) -> Vec<(String, Origin)> {
        let mut found: Vec<(String, Origin)> = Vec::new();
        for pod in pods {
            let (Some(spec), Some(status)) = (&pod.spec, &pod.status) else {
                continue;
            };
            let statuses = status.container_statuses.as_deref().unwrap_or_default();
            for container in &spec.containers {
                let status = statuses.iter().find(|status| status.name == container.name);
                let origin = status.and_then(|status| self.of(pod, status));
                let (Some(image), Some(origin)) = (&container.image, origin) else {
                    continue;
                };
                let entry = (image.clone(), origin);
                if !found.contains(&entry) {
                    found.push(entry);
                }
            }
        }
        found
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use rstest::rstest;

    use super::super::ImageRef;
    use super::super::reference::fixtures::digest;
    use super::*;

    fn image(c: char) -> ImageRef {
        ImageRef {
            registry: "registry.example".into(),
            repository: "team/app".into(),
            digest: digest(c),
        }
    }

    fn pod(name: &str, c: char) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}}}\n\
             spec: {{containers: [{{name: app, image: 'registry.example/team/app:1'}}]}}\n\
             status: {{containerStatuses: [{{name: app, ready: true, restartCount: 0, image: app, imageID: 'registry.example/team/app@{}'}}]}}",
            digest(c)
        ))
        .unwrap()
    }

    fn read(source: Option<&str>, revision: Option<&str>) -> Lookup {
        Lookup::Read(Arc::new(Provenance {
            source: source.map(Into::into),
            revision: revision.map(Into::into),
            version: Some("1.0".into()),
            ..Provenance::default()
        }))
    }

    fn built(commit: Option<Commit>, source: Option<&str>) -> Origin {
        Origin::Read(Built {
            commit,
            source: source.map(Into::into),
            version: Some("1.0".into()),
            description: None,
            documentation: None,
        })
    }

    #[rstest]
    #[case::a_commit_on_a_known_forge(
        read(Some("https://github.com/acme/app"), Some("abc1234")),
        built(
            Some(Commit::Link { href: "https://github.com/acme/app/commit/abc1234".into(), text: "app@abc1234".into() }),
            Some("https://github.com/acme/app")
        )
    )]
    #[case::a_source_without_a_revision(
        read(Some("https://github.com/acme/app"), None),
        built(
            Some(Commit::Text { source: "https://github.com/acme/app".into(), revision: String::new() }),
            Some("https://github.com/acme/app")
        )
    )]
    #[case::no_source(read(None, Some("abc1234")), built(None, None))]
    #[case::not_read_yet(Lookup::Reading, Origin::Reading)]
    #[case::unreadable(
        Lookup::Failed(Failure::NotFound),
        Origin::Unreadable(Failure::NotFound)
    )]
    fn reads_a_containers_origin(#[case] lookup: Lookup, #[case] expected: Origin) {
        let known = Known::default().with(&image('a'), lookup);
        let origins = Origins {
            known: &known,
            forges: &Forges::build(&crate::forge::fixtures::layouts(), []),
        };
        let found = origins.of_pods([&pod("web-1", 'a')]);
        assert_eq!(
            found,
            vec![("registry.example/team/app:1".into(), expected)]
        );
    }

    #[rstest]
    fn a_finished_pod_has_no_origin_to_read() {
        let known = Known::default();
        let origins = Origins {
            known: &known,
            forges: &Forges::build(&crate::forge::fixtures::layouts(), []),
        };
        let mut done = pod("job-1", 'a');
        done.status.as_mut().unwrap().phase = Some("Succeeded".into());
        assert_eq!(origins.of_pods([&done]), vec![]);
    }

    #[rstest]
    fn lists_each_origin_of_an_image_once() {
        let known = Known::default()
            .with(
                &image('a'),
                read(Some("https://github.com/acme/app"), Some("aaa")),
            )
            .with(
                &image('b'),
                read(Some("https://github.com/acme/app"), Some("bbb")),
            );
        let origins = Origins {
            known: &known,
            forges: &Forges::build(&crate::forge::fixtures::layouts(), []),
        };
        let pods = [pod("web-1", 'a'), pod("web-2", 'a'), pod("web-3", 'b')];
        assert_eq!(origins.of_pods(&pods).len(), 2);
    }
}
