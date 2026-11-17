//! A count of the running images by registry: how many have no source
//! label, and how many transect cannot read and why.  The cluster's
//! findings show these counts.  transect counts per registry, so the
//! counts for images built locally stay separate from the counts for
//! images pulled from elsewhere.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;

use super::{Failure, ImageRef, Known, Lookup, parse};
use transect_graph::pods::finished;

/// One registry's running images without a source label.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Gap {
    pub registry: String,
    /// How many have no source label.
    pub unlabelled: usize,
    /// How many transect has read the labels of.
    pub read: usize,
}

/// One registry's running images transect cannot read, for one reason.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Unreadable {
    pub registry: String,
    pub images: usize,
    pub failure: Failure,
    /// The mirror transect read the registry's host from, as a finding
    /// names it.
    pub mirror: Option<String>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Census {
    /// The registries with an unlabelled image, the most first.
    pub gaps: Vec<Gap>,
    /// Each registry and reason, by registry and then by reason.
    pub unreadable: Vec<Unreadable>,
}

/// Counts the images that the unfinished pods in `pods` run, and counts
/// each digest once.
pub fn census(pods: &[Arc<Pod>], known: &Known) -> Census {
    let images: BTreeSet<ImageRef> = pods
        .iter()
        .filter(|pod| !finished(pod))
        .filter_map(|pod| pod.status.as_ref())
        .flat_map(|status| {
            status
                .container_statuses
                .iter()
                .chain(&status.init_container_statuses)
        })
        .flatten()
        .filter_map(|status| parse(&status.image_id))
        .collect();
    let mut read: BTreeMap<&str, (usize, usize)> = BTreeMap::new();
    let mut unreadable: BTreeMap<(&str, String), (usize, Failure)> = BTreeMap::new();
    for image in &images {
        match known.lookup(image) {
            Lookup::Read(provenance) => {
                let counts = read.entry(&image.registry).or_default();
                counts.0 += 1;
                counts.1 += usize::from(provenance.source.is_none());
            }
            Lookup::Failed(failure) => {
                let key = (image.registry.as_str(), failure.note());
                unreadable.entry(key).or_insert((0, failure)).0 += 1;
            }
            Lookup::Reading => {}
        }
    }
    let mut gaps: Vec<Gap> = read
        .into_iter()
        .filter(|(_, (_, unlabelled))| *unlabelled > 0)
        .map(|(registry, (read, unlabelled))| Gap {
            registry: registry.into(),
            unlabelled,
            read,
        })
        .collect();
    gaps.sort_by_key(|gap| std::cmp::Reverse(gap.unlabelled));
    // Sort by registry, then by failure, in the order of the map's keys.
    // A count changes when transect reads images again, so an order by
    // count would move the lines between two renders.
    let unreadable: Vec<Unreadable> = unreadable
        .into_iter()
        .map(|((registry, _), (images, failure))| Unreadable {
            registry: registry.into(),
            images,
            failure,
            mirror: known.mirror_of(registry).map(ToString::to_string),
        })
        .collect();
    Census { gaps, unreadable }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Provenance;
    use super::super::reference::fixtures::digest;
    use super::*;

    fn image(registry: &str, c: char) -> ImageRef {
        ImageRef {
            registry: registry.into(),
            repository: "team/app".into(),
            digest: digest(c),
        }
    }

    /// A pod in `phase` whose one container runs `image`.
    fn pod(name: &str, phase: &str, image: &ImageRef) -> Arc<Pod> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "metadata: {{namespace: shop, name: {name}}}\n\
                 spec: {{containers: [{{name: app}}]}}\n\
                 status: {{phase: {phase}, containerStatuses: [{{name: app, ready: true, restartCount: 0, image: app, \
                 imageID: '{}/{}@{}'}}]}}",
                image.registry, image.repository, image.digest
            ))
            .unwrap(),
        )
    }

    fn read(source: Option<&str>) -> Lookup {
        Lookup::Read(Arc::new(Provenance {
            source: source.map(Into::into),
            ..Provenance::default()
        }))
    }

    #[rstest]
    fn counts_unlabelled_and_unreadable_images_by_registry() {
        let (a, b, c) = (
            image("registry.example", 'a'),
            image("registry.example", 'b'),
            image("registry.example", 'c'),
        );
        let (d, e) = (image("private.example", 'd'), image("private.example", 'e'));
        let done = image("registry.example", 'f');
        let pods = [
            pod("web-1", "Running", &a),
            pod("web-2", "Running", &a),
            pod("web-3", "Running", &b),
            pod("web-4", "Running", &c),
            pod("db-1", "Running", &d),
            pod("db-2", "Running", &e),
            pod("job-1", "Succeeded", &done),
        ];
        let known = Known::default()
            .with(&a, read(None))
            .with(&b, read(None))
            .with(&c, read(Some("https://github.com/acme/app")))
            .with(&d, Lookup::Failed(Failure::NeedsCredentials))
            .with(&e, Lookup::Failed(Failure::NeedsCredentials))
            .with(&done, read(None));
        let expected = Census {
            gaps: vec![Gap {
                registry: "registry.example".into(),
                unlabelled: 2,
                read: 3,
            }],
            unreadable: vec![Unreadable {
                registry: "private.example".into(),
                images: 2,
                failure: Failure::NeedsCredentials,
                mirror: None,
            }],
        };
        assert_eq!(census(&pods, &known), expected);
    }

    #[rstest]
    fn names_the_mirror_an_unreadable_registry_was_read_from() {
        let a = image("os.example", 'a');
        let pods = [pod("agent-1", "Running", &a)];
        let mirrors = super::super::mirror::Mirrors::from([(
            "os.example".to_string(),
            super::super::mirror::Mirror::ApiServer,
        )]);
        let known = Known::default()
            .with(&a, Lookup::Failed(Failure::NotFound))
            .with_mirrors(mirrors);
        let found = census(&pods, &known).unreadable;
        let mirrors: Vec<Option<&str>> = found.iter().map(|u| u.mirror.as_deref()).collect();
        assert_eq!(mirrors, vec![Some("the API server's registry")]);
    }

    #[rstest]
    fn lists_unreadable_registries_by_name_whatever_their_counts() {
        let (a, b, c) = (
            image("zeta.example", 'a'),
            image("zeta.example", 'b'),
            image("alpha.example", 'c'),
        );
        let pods = [
            pod("web-1", "Running", &a),
            pod("web-2", "Running", &b),
            pod("db-1", "Running", &c),
        ];
        let known = Known::default()
            .with(&a, Lookup::Failed(Failure::NotFound))
            .with(&b, Lookup::Failed(Failure::NotFound))
            .with(&c, Lookup::Failed(Failure::NeedsCredentials));
        let found = census(&pods, &known).unreadable;
        let registries: Vec<&str> = found.iter().map(|u| u.registry.as_str()).collect();
        assert_eq!(registries, vec!["alpha.example", "zeta.example"]);
    }

    #[rstest]
    fn counts_nothing_before_any_read() {
        let pods = [pod("web-1", "Running", &image("registry.example", 'a'))];
        assert_eq!(census(&pods, &Known::default()), Census::default());
    }
}
