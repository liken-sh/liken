//! What intent can be written on in one snapshot, where a CSI driver
//! stores its data, and the conditions this reading of the cluster
//! gives `transect`'s resources.

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::Resource;

use super::Snapshot;
use transect_graph::edges::Object;
use transect_ground::storage::OnMachines;
use transect_resources::intent;
use transect_resources::knowledge::Names;
use transect_resources::resources::{Condition, DocId};

impl Snapshot {
    /// The CSI drivers whose `data-on` intent is `machines`, each with
    /// where that intent was written.
    pub(super) fn on_machines(&self) -> OnMachines {
        let drivers = self.csi_drivers.iter();
        let names = drivers.filter_map(|driver| driver.metadata.name.as_deref());
        names
            .filter_map(|name| {
                let object = Object::new("storage.k8s.io", "CSIDriver", None, name);
                let line = self.intents.of(&object)?.data_on.as_ref()?;
                let machines = line.text.trim() == "machines";
                machines.then(|| (name.to_string(), line.provenance.clone()))
            })
            .collect()
    }

    /// Each CSI driver's console URLs, from the `console` template its
    /// intent gives, filled from its volumes and its StorageClasses.
    pub fn consoles(&self) -> HashMap<String, Vec<String>> {
        let templates = self.csi_drivers.iter().filter_map(|driver| {
            let name = driver.metadata.name.as_deref()?;
            let object = Object::new("storage.k8s.io", "CSIDriver", None, name);
            let line = self.intents.of(&object)?.console.as_ref()?;
            Some((name.to_string(), line.text.trim().to_string()))
        });
        let paged = &self.paged;
        transect_ground::storage::consoles(
            &templates.collect(),
            &paged.volumes,
            &paged.storage_classes,
        )
    }

    /// Every object intent can be written on, with its metadata: the
    /// namespaces, the machines, the workloads, the Services, Ingresses,
    /// PodDisruptionBudgets, and NetworkPolicies in front of and around
    /// them, the ConfigMaps they read, the Secrets when their
    /// annotations are read, the ServiceAccounts they run as, the CSI
    /// drivers, and the custom kinds that own pods.  Pods are left out,
    /// because a pod's card shows its workload's intent.
    pub fn described(&self) -> Vec<(Object, &ObjectMeta)> {
        fn of<K: Resource<DynamicType = ()>>(objects: &[Arc<K>]) -> Vec<(Object, &ObjectMeta)> {
            let (group, kind) = (K::group(&()), K::kind(&()));
            objects
                .iter()
                .filter_map(|object| {
                    let meta = object.meta();
                    let name = meta.name.as_deref()?;
                    Some((
                        Object::new(&group, &kind, meta.namespace.as_deref(), name),
                        meta,
                    ))
                })
                .collect()
        }
        let custom = self.custom.iter().flat_map(|(resource, metas)| {
            metas.iter().filter_map(|meta| {
                let name = meta.metadata.name.as_deref()?;
                let namespace = meta.metadata.namespace.as_deref();
                let object = Object::new(&resource.group, &resource.kind, namespace, name);
                Some((object, &meta.metadata))
            })
        });
        [
            of(&self.namespaces),
            of(&self.nodes),
            of(&self.deployments),
            of(&self.stateful_sets),
            of(&self.daemon_sets),
            of(&self.cron_jobs),
            of(&self.services),
            of(&self.ingresses),
            of(&self.budgets),
            of(&self.network_policies),
            of(&self.config_maps),
            of(self.secrets.as_deref().unwrap_or_default()),
            of(&self.paged.service_accounts),
            of(&self.csi_drivers),
        ]
        .into_iter()
        .flatten()
        .chain(custom)
        .collect()
    }

    /// A condition on the kind knowledge of each object whose reference
    /// by value matched more than one object, so the graph has no edge
    /// for it.
    fn ambiguous_matches(&self) -> Vec<(DocId, Condition)> {
        let mut found: Vec<(DocId, Condition)> = Vec::new();
        for (object, relations) in &self.relations {
            let Some(known) = self.kinds.of(&object.group, &object.kind) else {
                continue;
            };
            for relation in relations {
                let Names::Ambiguous(count) = relation.names else {
                    continue;
                };
                let named = transect_graph::links::kind_and_name_in(object, None);
                let message = format!(
                    "{named}'s {} is {}, which matches {count} objects, so transect draws none of them",
                    relation.path, relation.value
                );
                found.push((known.doc, Condition::ambiguous(message)));
            }
        }
        found.sort_by(|a, b| a.1.message.cmp(&b.1.message));
        found
    }

    /// The conditions on `transect`'s resources that this reading of the
    /// cluster gives: an overlay, a layer's selector, or an `Intent`'s
    /// term that names nothing here, a list of intent on an object it
    /// draws nothing from, and kind knowledge for a kind the
    /// cluster does not serve or a layer it does not declare.
    pub fn conditions(&self) -> Vec<(DocId, Condition)> {
        let mut conditions = self.overlaid.unmatched();
        conditions.extend(self.intents.conditions.iter().cloned());
        conditions.extend(self.kinds.conditions.iter().cloned());
        conditions.extend(self.outsides.conditions.iter().cloned());
        conditions.extend(self.log_conditions());
        conditions.extend(self.ambiguous_matches());
        let layers = &self.resources.layers;
        if let Some(doc) = layers.doc {
            let namespaces = self.namespaces.iter().map(|namespace| &namespace.metadata);
            let unmatched = layers.unmatched(namespaces);
            conditions.extend(unmatched.into_iter().map(|condition| (doc, condition)));
        }
        let written = self.secrets_written();
        conditions.extend(intent::unattached(
            self.intents.iter(),
            intent::Written {
                overlays: &self.resources.directory.overlays,
                intents: &self.resources.intents,
            },
            |object| {
                transect_network::addresses::attaches(object, &self.ownership)
                    || written.contains_key(object)
            },
        ));
        let described = self.described();
        conditions.extend(intent::unmatched(
            &self.resources.intents,
            &described,
            &self.plurals,
        ));
        conditions
    }
}
