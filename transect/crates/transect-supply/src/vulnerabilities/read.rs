//! Reads the scanners' reports from the stores of custom resources into
//! the index that the pages render.  Reports repeat the same ids,
//! packages, and versions, so the index keeps one shared copy of each
//! string.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use kube::api::ApiResource;
use serde_json::Value;

use super::{Finding, Report, Vulnerabilities};
use transect_cluster::cluster::Custom;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;
use transect_resources::knowledge::findings::Findings;
use transect_resources::knowledge::{KindKnowledge, Kinds, MetaKey, path};

/// The most characters kept of one field of a finding.  A longer field
/// is cut and ends with an ellipsis.
const FIELD_CHARS: usize = 200;

/// The stores that the index is read from.
pub struct Stores<'a> {
    pub knowledge: &'a [KindKnowledge],
    pub kinds: &'a Kinds,
    pub custom: &'a [(ApiResource, Vec<Arc<Custom>>)],
    pub ownership: &'a Ownership,
    pub pods: &'a [Arc<Pod>],
}

/// The image digests that each workload's unfinished pods run, by
/// container name, read from the `imageID` of each container status,
/// init containers included.
fn running_digests(
    pods: &[Arc<Pod>],
    ownership: &Ownership,
) -> HashMap<Object, HashMap<String, HashSet<String>>> {
    let mut running: HashMap<Object, HashMap<String, HashSet<String>>> = HashMap::new();
    for pod in pods
        .iter()
        .filter(|pod| !transect_graph::pods::finished(pod))
    {
        let meta = &pod.metadata;
        let Some(name) = meta.name.as_deref() else {
            continue;
        };
        let object = Object::new("", "Pod", meta.namespace.as_deref(), name);
        let owner = ownership.top_owner(&object);
        let statuses = pod.status.iter().flat_map(|status| {
            let app = status.container_statuses.iter().flatten();
            app.chain(status.init_container_statuses.iter().flatten())
        });
        for status in statuses {
            let Some(digest) = digest_of(&status.image_id) else {
                continue;
            };
            let containers = running.entry(owner.clone()).or_default();
            containers
                .entry(status.name.clone())
                .or_default()
                .insert(digest.to_string());
        }
    }
    running
}

/// The digest in an image id such as `registry/app@sha256:…`.
fn digest_of(image_id: &str) -> Option<&str> {
    let digest = image_id.rsplit('@').next()?;
    digest.starts_with("sha256:").then_some(digest)
}

impl Vulnerabilities {
    pub fn read(stores: Stores) -> Self {
        let described: Vec<&KindKnowledge> = stores
            .knowledge
            .iter()
            .filter(|known| known.findings.is_some())
            .collect();
        let mut severities: Vec<Arc<str>> = Vec::new();
        let mut strings = Strings::default();
        for known in &described {
            for one in &known.findings.as_ref().unwrap().summary {
                let severity = strings.get(&one.severity);
                if !severities.contains(&severity) {
                    severities.push(severity);
                }
            }
        }
        let running = running_digests(stores.pods, stores.ownership);
        let mut reports = Vec::new();
        for (resource, objects) in stores.custom {
            let Some(known) = stores.kinds.of(&resource.group, &resource.kind) else {
                continue;
            };
            let Some(findings) = &known.findings else {
                continue;
            };
            let scanner = strings.get(&scanner(known, &resource.kind));
            for object in objects {
                let read = Read {
                    resource,
                    findings,
                    scanner: &scanner,
                    ownership: stores.ownership,
                    running: &running,
                };
                reports.extend(read.report(object, &mut strings));
            }
        }
        reports.sort_by(|a: &Report, b: &Report| {
            (&a.workload, &a.container, &a.object).cmp(&(&b.workload, &b.container, &b.object))
        });
        let mut by_id: std::collections::BTreeMap<Arc<str>, Vec<(usize, usize)>> =
            Default::default();
        for (at, report) in reports.iter().enumerate().filter(|(_, r)| r.current) {
            for (finding, one) in report.findings.iter().enumerate() {
                by_id.entry(one.id.clone()).or_default().push((at, finding));
            }
        }
        Self {
            described: !described.is_empty(),
            reports,
            severities,
            by_id,
        }
    }
}

/// The scanner's name for a report kind: the last path segment of the
/// kind knowledge's `controller_image`, or the kind when it has none.
fn scanner(known: &KindKnowledge, kind: &str) -> String {
    let image = known.controller_image.as_deref();
    let last = image.and_then(|image| image.rsplit('/').next());
    last.filter(|last| !last.is_empty())
        .unwrap_or(kind)
        .to_string()
}

struct Read<'a> {
    resource: &'a ApiResource,
    findings: &'a Findings,
    scanner: &'a Arc<str>,
    ownership: &'a Ownership,
    running: &'a HashMap<Object, HashMap<String, HashSet<String>>>,
}

impl Read<'_> {
    /// `object` read as a report, or `None` when it has no name or no
    /// owner reference.
    fn report(&self, object: &Custom, strings: &mut Strings) -> Option<Report> {
        let meta = &object.metadata;
        let namespace = meta.namespace.as_deref();
        let name = meta.name.as_deref()?;
        let owners = meta.owner_references.iter().flatten();
        let owner = owners
            .clone()
            .find(|owner| owner.controller == Some(true))
            .or_else(|| owners.clone().next())?;
        let group = owner.api_version.split_once('/').map(|(group, _)| group);
        let owner = Object::new(
            group.unwrap_or_default(),
            &owner.kind,
            namespace,
            &owner.name,
        );
        let workload = self.ownership.top_owner(&owner);
        let container = self.findings.container.as_ref().and_then(|key| {
            let found = match key {
                MetaKey::Label(key) => meta.labels.as_ref()?.get(key),
                MetaKey::Annotation(key) => meta.annotations.as_ref()?.get(key),
            };
            found.cloned()
        });
        let data = &object.data;
        let first = |path: &str| path::read(data, path).into_iter().next();
        let digest = first(&self.findings.digest);
        let scanned_at = self.findings.scanned_at.as_deref().and_then(first);
        let scanned_at = scanned_at.and_then(|at| at.parse().ok());
        let summary = self.findings.summary.iter().map(|one| {
            let count = first(&one.path).and_then(|count| count.parse().ok());
            (strings.get(&one.severity), count.unwrap_or(0))
        });
        let summary = summary.collect();
        let current = digest.as_deref().is_some_and(|digest| {
            let running = self.running.get(&workload);
            let mut containers = running.into_iter().flatten();
            containers.any(|(name, digests)| {
                let named = container.as_deref().is_none_or(|c| c == name);
                named && digests.contains(digest)
            })
        });
        let items = path::values(data, &self.findings.items);
        let findings = items
            .into_iter()
            .filter_map(|item| self.finding(item, strings))
            .collect();
        Some(Report {
            object: Object::new(&self.resource.group, &self.resource.kind, namespace, name),
            scanner: self.scanner.clone(),
            workload,
            container,
            digest,
            scanned_at,
            summary,
            current,
            findings,
        })
    }

    /// One finding, or `None` when it has no id or no severity.
    fn finding(&self, item: &Value, strings: &mut Strings) -> Option<Finding> {
        let fields = &self.findings.item;
        let mut field = |path: Option<&String>| {
            let value = path::read(item, path?).into_iter().next()?;
            let value = transect_graph::untrusted::capped(&value, FIELD_CHARS)?;
            (!value.is_empty()).then(|| strings.get(&value))
        };
        let link = field(fields.link.as_ref());
        let link = link.filter(|link| transect_graph::untrusted::link(link).is_some());
        Some(Finding {
            id: field(Some(&fields.id))?,
            severity: field(Some(&fields.severity))?,
            score: field(fields.score.as_ref()),
            package: field(fields.package.as_ref()),
            installed: field(fields.installed.as_ref()),
            fixed: field(fields.fixed.as_ref()),
            link,
            title: field(fields.title.as_ref()),
        })
    }
}

/// The strings read so far, so that equal strings share one allocation.
#[derive(Default)]
struct Strings(HashSet<Arc<str>>);

impl Strings {
    fn get(&mut self, text: &str) -> Arc<str> {
        if let Some(held) = self.0.get(text) {
            return held.clone();
        }
        let held: Arc<str> = text.into();
        self.0.insert(held.clone());
        held
    }
}
