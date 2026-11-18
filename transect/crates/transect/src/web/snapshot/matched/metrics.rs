//! Marks the `MetricsSource` as unmatched when a `Service` it names
//! does not exist, or when its queries return series whose labels
//! identify no `Node`, pod, or claim.

use super::super::Snapshot;
use transect_graph::edges::Claim;
use transect_graph::upstream::Service;
use transect_resources::resources::{Condition, DocId};

impl Snapshot {
    pub(super) fn unmatched_metrics(
        &self,
        usage: &transect_observe::usage::Reading,
    ) -> Vec<(DocId, Condition)> {
        let Some(source) = &self.resources.metrics_source else {
            return Vec::new();
        };
        let mut messages = Vec::new();
        let services = [
            ("service", source.service.as_ref()),
            (
                "alerts.service",
                source
                    .alerts
                    .as_ref()
                    .and_then(|alerts| alerts.service.as_ref()),
            ),
        ];
        for (field, service) in services {
            if let Some(service) = service
                && !self.has_service(service)
            {
                messages.push(format!(
                    "{field} names the Service {}/{}, which the cluster does not have",
                    service.namespace, service.name
                ));
            }
        }
        if let transect_observe::usage::Reading::Read(read) = usage {
            let queries = &source.usage;
            let machines = queries.machine_cpu.is_some() || queries.machine_memory.is_some();
            let nodes = self.nodes.iter();
            let named = nodes
                .filter_map(|node| node.metadata.name.as_deref())
                .any(|node| read.of(node) != Default::default());
            if machines && !named {
                messages.push(format!(
                    "the machine queries' series name no Node by nodeLabels {}",
                    queries.node_labels.join(", ")
                ));
            }
            let containers = queries.container_cpu.is_some() || queries.container_memory.is_some();
            let pods = self.pods.iter().filter_map(|pod| {
                let meta = &pod.metadata;
                Some((meta.namespace.as_deref()?, meta.name.as_deref()?))
            });
            let named = pods
                .into_iter()
                .any(|(namespace, name)| read.of_pod(namespace, name) != Default::default());
            if containers && !named {
                messages.push("the container queries' series name no pod by their labels".into());
            }
        }
        if let transect_observe::metrics::Reading::Read(used) = &self.volumes_used {
            let claims = self.paged.claims.iter().filter_map(|claim| {
                let meta = &claim.metadata;
                Some(Claim::new(
                    meta.namespace.as_deref()?,
                    meta.name.as_deref()?,
                ))
            });
            let named = claims.into_iter().any(|claim| used.contains_key(&claim));
            if !used.is_empty() && !named {
                messages.push("the volumeUsed query's series name no claim by its labels".into());
            }
        }
        messages
            .into_iter()
            .map(|message| (source.doc, Condition::unmatched(message)))
            .collect()
    }

    fn has_service(&self, service: &Service) -> bool {
        self.services.iter().any(|found| {
            let meta = &found.metadata;
            meta.namespace.as_deref() == Some(service.namespace.as_str())
                && meta.name.as_deref() == Some(service.name.as_str())
        })
    }
}
