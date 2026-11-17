//! The namespace card a reading gives, and the forges it lists.

use std::collections::HashMap;

use super::Snapshot;
use transect_cluster::ownership::{self, Managed};
use transect_cluster::workloads::Workloads;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_ground::devices::Holds;
use transect_map::cards::{NamespaceCard, owned_key};
use transect_network::addresses::{Across, Depends};
use transect_network::policy::{Allowed, Calls};
use transect_observe::health::{self, Health};
use transect_supply::findings::Finding;
use transect_supply::forge::Forges;
use transect_supply::gitops::CodeSection;

use super::logged;

impl Snapshot {
    /// The forges the resources declare, with those the cluster serves
    /// itself: each `Ingress` host, with the images of the workloads
    /// behind the Services it routes to.
    pub fn forges(&self, graph: &Graph, workloads: &Workloads) -> Forges {
        let mut hosts = Vec::new();
        for ingress in &self.ingresses {
            let Some(namespace) = ingress.metadata.namespace.as_deref() else {
                continue;
            };
            let spec = ingress.spec.iter();
            for rule in spec.flat_map(|spec| spec.rules.iter().flatten()) {
                let paths = rule.http.iter().flat_map(|http| &http.paths);
                let services = paths.filter_map(|path| path.backend.service.as_ref());
                let images = services
                    .flat_map(|service| {
                        let object = Object::new("", "Service", Some(namespace), &service.name);
                        graph.leaving(Kind::Selects, &Endpoint::Object(object))
                    })
                    .filter_map(|end| match end {
                        Endpoint::Object(object) => Some(workloads.get(object).images()),
                        _ => None,
                    })
                    .flatten()
                    .collect();
                hosts.extend(rule.host.clone().map(|host| (host, images)));
            }
        }
        Forges::build(&self.resources.forges, hosts)
    }

    /// The card of `namespace`.  `forges` are those the cluster serves,
    /// and `found` the log findings, of which the card lists those in
    /// `namespace`.
    pub fn card(
        &self,
        namespace: &str,
        health: Health,
        graph: &Graph,
        (forges, found): (&Forges, &[Finding]),
        allowed: &Allowed,
    ) -> NamespaceCard {
        let across = Across::of(graph, namespace);
        let code = CodeSection::of_namespace(
            graph,
            &self.gitops,
            forges,
            self.ownership.workloads(namespace),
            namespace,
        )
        .linked(|object| self.pages().href(object));
        let by_workload = health::by_workload(&health, namespace, &self.ownership);
        let namespace_object = Object::new("", "Namespace", None, namespace);
        // A built-in workload's chip names it alone; any other kind's
        // names its kind too, so a `Player` and a `Remote` of one name
        // each keep their own title.
        let mut whys = HashMap::new();
        for workload in self.ownership.workloads(namespace) {
            let why = self.intents.of(workload).and_then(|said| said.why.as_ref());
            let why = why.map(|why| why.text.clone()).or_else(|| {
                let knowledge = self.kinds.of_object(workload)?;
                knowledge.summary.clone()
            });
            let why = why.or_else(|| Some(self.definitions.of(workload)?.text));
            let Some(why) = why else { continue };
            if !ownership::custom(workload) {
                whys.insert(workload.name.clone(), why.clone());
            }
            whys.insert(owned_key(&workload.kind, &workload.name), why);
        }
        NamespaceCard::build(
            namespace,
            health,
            &self.objects(),
            &self.ingresses,
            self.ownership.workloads(namespace),
            &by_workload,
            self.storage.claims_in(namespace),
            |workload| self.pages().href(workload),
        )
        .with_depends(Depends::of_namespace(&across))
        .with_managed(Managed::of_namespace(
            graph,
            namespace,
            &self.grants.missing,
        ))
        .with_code(code)
        .with_calls(Calls::of_namespace(allowed, namespace, |workload| {
            self.pages().href(workload)
        }))
        .with_holds(Holds::of_namespace(
            graph,
            namespace,
            |workload| self.pages().href(workload),
            |device| self.devices.named(device, &self.resources.devices),
        ))
        .with_intent(self.intents.of(&namespace_object), &whys)
        .with_layer_named(self.layer_named(namespace, &self.resources.layers))
        .with_logged(logged::worth_knowing_in(found, namespace))
        .with_reason_pages(|reason| {
            reason.page(namespace, &self.ownership, &namespace_object, |object| {
                self.pages().href(object)
            })
        })
    }
}
