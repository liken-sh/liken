//! Reads the pod templates of every object whose kind's knowledge
//! states `templates`, and of the built-in workloads, into the form
//! that `transect_access::access::presents` draws edges from.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::core::v1::{PodSpec, PodTemplateSpec};
use serde_json::Value;

use super::Snapshot;
use transect_access::access::presents::{Audience, Presented, Runner};
use transect_cluster::workloads::Workloads;
use transect_graph::edges::Object;
use transect_graph::tokens;
use transect_map::presents::{Accepting, Presents};
use transect_resources::knowledge::templates::Template;
use transect_resources::knowledge::{Followed, ReferenceEnds, path};

/// What one template presents before the audiences become a list: each
/// audience with the objects that a label names for it.
type Found = BTreeMap<String, Vec<Object>>;

impl Snapshot {
    /// Every template that the knowledge of a kind states on an object,
    /// and every built-in workload's template that presents an audience
    /// besides the API server's.  A custom resource's template is listed
    /// even when it presents nothing, because the page of its account
    /// shows what runs as the account.
    pub(super) fn read_presented(&self) -> Vec<Presented> {
        let mut presented = self.custom_templates();
        presented.extend(self.workload_templates());
        presented
    }

    fn custom_templates(&self) -> Vec<Presented> {
        let mut presented = Vec::new();
        for (resource, objects) in &self.custom {
            let known = self.kinds.of(&resource.group, &resource.kind);
            let Some(known) = known.filter(|known| !known.templates.is_empty()) else {
                continue;
            };
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let object = Object::new(
                    &resource.group,
                    &resource.kind,
                    meta.namespace.as_deref(),
                    name,
                );
                for template in &known.templates {
                    presented.extend(self.custom_template(&object, &found.data, template));
                }
            }
        }
        presented
    }

    /// The template at `template.path` of `object`, whose spec and
    /// status are `data`, merged with the template of the same use of
    /// the object that its `merge` reference names.  A field that is
    /// absent gives no template unless a merged template fills it,
    /// because the controller then makes its pods from the merged
    /// template alone.
    fn custom_template(
        &self,
        object: &Object,
        data: &Value,
        template: &Template,
    ) -> Option<Presented> {
        let own = path::values(data, &template.path).into_iter().next();
        let mut found = Found::new();
        if let Some(own) = own {
            self.read_template(&mut found, object, own);
        }
        let mut merged = false;
        let mut merges = Vec::new();
        if let Some(merge) = &template.merge {
            for end in self.ends(object, merge) {
                let Followed::Found(other, other_data) = end else {
                    continue;
                };
                merges.push(other.clone());
                let Some(known) = self.kinds.of_object(&other) else {
                    continue;
                };
                let same = known.templates.iter().filter(|t| t.used == template.used);
                for theirs in same {
                    merged = true;
                    let value = path::values(&other_data.data, &theirs.path)
                        .into_iter()
                        .next();
                    if let Some(value) = value {
                        self.read_template(&mut found, object, value);
                    }
                    for audience in &theirs.injects {
                        found.entry(audience.clone()).or_default();
                    }
                }
            }
        }
        if own.is_none() && !merged {
            return None;
        }
        for audience in &template.injects {
            found.entry(audience.clone()).or_default();
        }
        let runs_as = template
            .runs_as
            .as_ref()
            .and_then(|field| path::read_of(object, data, field).into_iter().next());
        Some(Presented {
            holder: object.clone(),
            path: template.path.clone(),
            used: Some(template.used.clone()),
            runs_as,
            audiences: audiences(found),
            merges,
        })
    }

    /// Adds to `found` the audiences that `template`, a raw pod template
    /// of `holder`, mounts, and those that its labels ask a program for.
    fn read_template(&self, found: &mut Found, holder: &Object, template: &Value) {
        for audience in tokens::template_audiences(template) {
            found.entry(audience).or_default();
        }
        self.labelled(found, holder, &tokens::template_labels(template));
    }

    /// Adds to `found` the audience of each program that injects one by
    /// a label in `labels`, with the object that the label's value
    /// names, in `holder`'s namespace.
    fn labelled(&self, found: &mut Found, holder: &Object, labels: &[(String, String)]) {
        for (injects, value) in self.programs.injected(labels) {
            let names = found.entry(injects.audience.clone()).or_default();
            if let Some(kind) = &injects.names {
                let named =
                    Object::new(&kind.group, &kind.kind, holder.namespace.as_deref(), value);
                if !names.contains(&named) {
                    names.push(named);
                }
            }
        }
    }

    /// The template of each `Deployment`, `StatefulSet`, `DaemonSet`, and
    /// `CronJob` that presents an audience.
    fn workload_templates(&self) -> Vec<Presented> {
        let deployments = self
            .deployments
            .iter()
            .map(|o| ("Deployment", &o.metadata, Some(&o.spec.template)));
        let stateful = self
            .stateful_sets
            .iter()
            .map(|o| ("StatefulSet", &o.metadata, Some(&o.spec.template)));
        let daemons = self
            .daemon_sets
            .iter()
            .map(|o| ("DaemonSet", &o.metadata, Some(&o.spec.template)));
        let apps = deployments
            .chain(stateful)
            .chain(daemons)
            .map(|(kind, meta, template)| ("apps", kind, meta, "spec.template", template));
        let crons = self.cron_jobs.iter().map(|o| {
            let template = o.spec.job_template.spec.as_ref().map(|s| &s.template);
            (
                "batch",
                "CronJob",
                &o.metadata,
                "spec.jobTemplate.spec.template",
                template,
            )
        });
        let mut presented = Vec::new();
        for (group, kind, meta, field, template) in apps.chain(crons) {
            let (Some(name), Some(template)) = (meta.name.as_deref(), template) else {
                continue;
            };
            let holder = Object::new(group, kind, meta.namespace.as_deref(), name);
            if let Some(found) = self.workload_template(&holder, field, template) {
                presented.push(found);
            }
        }
        presented
    }

    fn workload_template(
        &self,
        holder: &Object,
        field: &str,
        template: &PodTemplateSpec,
    ) -> Option<Presented> {
        let spec = template.spec.as_ref()?;
        let mut found = Found::new();
        for audience in tokens::audiences(spec) {
            found.entry(audience).or_default();
        }
        let labels = template.metadata.as_ref().and_then(|m| m.labels.as_ref());
        let labels: Vec<(String, String)> = labels
            .into_iter()
            .flatten()
            .map(|(k, v)| (k.clone(), v.clone()))
            .collect();
        self.labelled(&mut found, holder, &labels);
        if found.is_empty() {
            return None;
        }
        let account = spec
            .service_account_name
            .clone()
            .unwrap_or_else(|| "default".into());
        Some(Presented {
            holder: holder.clone(),
            path: field.into(),
            used: None,
            runs_as: Some(account),
            audiences: audiences(found),
            merges: Vec::new(),
        })
    }

    /// What the templates of each holder present, and the programs that
    /// accept those audiences, for the lanes of a sequence and the
    /// columns of a kind's objects.  A template that merges another
    /// object's template, as a `Duty`'s turn merges its `Mandate`'s,
    /// adds its audiences to that object too, because its pods do that
    /// object's work.
    pub fn read_presents(&self) -> BTreeMap<Object, Presents> {
        let presented = &self.presented;
        let mut audiences: BTreeMap<Object, BTreeSet<String>> = BTreeMap::new();
        for template in presented {
            let names = template.audiences.iter().map(|a| a.name.clone());
            let holders = std::iter::once(&template.holder).chain(&template.merges);
            for holder in holders {
                audiences
                    .entry(holder.clone())
                    .or_default()
                    .extend(names.clone());
            }
        }
        let runners = &self.runners;
        let mut accepting: Vec<Accepting> = Vec::new();
        for program in self.programs.iter() {
            let runs = runners
                .iter()
                .filter(|r| r.images.iter().any(|i| program.runs(i)));
            let runs: Vec<Object> = runs.map(|r| r.workload.clone()).collect();
            accepting.extend(program.accepts.iter().map(|accepts| Accepting {
                audience: accepts.audience.clone(),
                program: program.name.clone(),
                verb: accepts.verb.clone(),
                runs: runs.clone(),
            }));
        }
        let presents = audiences.into_iter().map(|(holder, audiences)| {
            let accepted = accepting.iter().filter(|a| audiences.contains(&a.audience));
            let presents = Presents {
                accepting: accepted.cloned().collect(),
                audiences: audiences.into_iter().collect(),
            };
            (holder, presents)
        });
        presents.collect()
    }

    /// Each workload with the images that its pods run, for the programs
    /// to match.  A workload whose top owner's kind states `templates` is
    /// left out, because its pods are made from those templates and so
    /// call the programs and do not serve them.  A sidecar of a program
    /// that a webhook adds to such a pod runs the program's image as a
    /// client.
    pub(super) fn read_runners(&self, workloads: &Workloads) -> Vec<Runner> {
        let mut runners = Vec::new();
        for workload in self.ownership.all_workloads() {
            let made = self
                .kinds
                .of_object(workload)
                .is_some_and(|known| !known.templates.is_empty());
            if made {
                continue;
            }
            let specs = workloads.get(workload).templates();
            let mut images: Vec<String> = specs.iter().flat_map(|spec| images(spec)).collect();
            images.sort();
            images.dedup();
            if !images.is_empty() {
                runners.push(Runner {
                    workload: workload.clone(),
                    images,
                });
            }
        }
        runners.sort_by(|a, b| a.workload.cmp(&b.workload));
        runners
    }
}

/// The images of `spec`'s containers and of its native sidecars, the
/// init containers with `restartPolicy: Always`, which run beside the
/// containers for the life of the pod.  A plain init container runs
/// to completion before the pod starts, such as one that copies a
/// program's binary, so it serves nothing.
fn images(spec: &PodSpec) -> Vec<String> {
    let containers = spec.containers.iter();
    let sidecars = spec.init_containers.iter().flatten();
    let sidecars = sidecars.filter(|c| c.restart_policy.as_deref() == Some("Always"));
    containers
        .chain(sidecars)
        .filter_map(|c| c.image.clone())
        .collect()
}

fn audiences(found: Found) -> Vec<Audience> {
    let audiences = found
        .into_iter()
        .map(|(name, names)| Audience { name, names });
    audiences.collect()
}
