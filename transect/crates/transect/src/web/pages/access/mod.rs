//! The pages of the access lens. They are built from the model's
//! reduced RBAC access and from the pages and intent of the objects
//! that they mention.

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::networking::v1::Ingress;

use super::super::model::Model;

mod account;
mod calls;
mod change;
mod dots;
mod followed;
mod holds;
mod look;
mod namespace;
mod served;
mod workload;

pub use account::{service_account_page, subject_page};
pub use namespace::namespace_access_page;
use transect_access::access::ways::{Isolation, Sources, Ways};
use transect_access::access::{AccountsPage, Look, WaysPage};
use transect_graph::edges::{Endpoint, Kind, Object, Shown};
use transect_network::policy::{NamespaceReach, Reach};
pub use workload::access_page;

/// Whether the workload `namespace/name` of the kind `resource` serves
/// a port. Only a workload that does has an access tab.
pub fn has_access(model: &Model, namespace: &str, resource: &str, name: &str) -> bool {
    let snapshot = &model.snapshot;
    let mut owners = snapshot.ownership.workloads(namespace);
    owners.any(|owner| {
        owner.name == name
            && transect_graph::links::kinds::workload(owner, &snapshot.plurals).as_deref()
                == Some(resource)
            && model.serves(owner)
    })
}

/// The page at `/access/accounts`: what each account may do, and where.
pub fn accounts_page(model: &Model) -> AccountsPage {
    let snapshot = &model.snapshot;
    let names = look::Look::new(model);
    let href = |object: &Object| names.href(object);
    let why = |object: &Object| snapshot.intents.of(object)?.why.clone();
    let look = Look {
        href: &href,
        why: &why,
    };
    AccountsPage::build(&model.access, &look)
}

/// The page at `/access`: every way into the cluster, with the network
/// policies and sign-ins that each one passes through.
pub fn ways_page(model: &Model) -> WaysPage {
    let snapshot = &model.snapshot;
    let allowed = model.allowed(&Shown::default());
    let open = snapshot.namespaces.iter().filter_map(|namespace| {
        let name = namespace.metadata.name.clone()?;
        matches!(allowed.of_namespace(&name), Some(NamespaceReach::Open)).then_some(name)
    });
    let names = look::Look::new(model);
    let href = |object: &Object| names.href(object);
    let look = transect_access::access::ways::page::Look {
        href: &href,
        open: open.collect(),
        unread: snapshot.unread_routes(),
        no_gitops: model.access.gitops.is_empty(),
    };
    WaysPage::build(&model.ways(), look)
}

/// Every way into the cluster from outside, read from the model's
/// snapshot.
pub fn read_ways(model: &Model) -> Ways {
    let snapshot = &model.snapshot;
    let graph = model.graph(&Shown::default());
    let allowed = model.allowed(&Shown::default());
    let objects = |endpoints: Vec<&Endpoint>| {
        let found = endpoints.into_iter().filter_map(|end| match end {
            Endpoint::Object(object) => Some(object.clone()),
            _ => None,
        });
        found.collect::<Vec<Object>>()
    };
    let selected = |service: &Object| {
        objects(graph.leaving(Kind::Selects, &Endpoint::Object(service.clone())))
    };
    let sign_ins = snapshot.route_sign_ins();
    // The workloads that the routes' sign-in objects authenticate
    // through. A route that reaches only one of these workloads is the
    // sign-in's own callback.
    let signs_in: BTreeSet<Object> = sign_ins
        .values()
        .flatten()
        .flat_map(|object| {
            objects(graph.leaving(
                Kind::AuthenticatesThrough,
                &Endpoint::Object(object.clone()),
            ))
        })
        .collect();
    let sign_in = |ingress: &Object| sign_ins.get(ingress).cloned().unwrap_or_default();
    let isolation = |workload: &Object| {
        Some(match allowed.reach(workload)? {
            Reach::Open => Isolation::Open,
            Reach::MachineNetwork => Isolation::MachineNetwork,
            Reach::Isolated(_) => Isolation::Isolated,
        })
    };
    let admissions = |workload: &Object| allowed.admissions(workload).to_vec();
    let workload_of = |pod: &Pod| {
        let meta = &pod.metadata;
        let object = Object::new("", "Pod", meta.namespace.as_deref(), meta.name.as_deref()?);
        Some(snapshot.ownership.top_owner(&object))
    };
    let declares = |object: &Object| snapshot.intents.of(object)?.signs_in.clone();
    let serving = snapshot.serving();
    let controller = |ingress: &Ingress| snapshot.ingress_controller(&serving, ingress);
    let sources = Sources {
        ingresses: &snapshot.ingresses,
        services: &snapshot.services,
        pods: &snapshot.pods,
        daemon_sets: &snapshot.daemon_sets,
        nodes: &snapshot.nodes,
        load_balancers: &snapshot.resources.load_balancers,
        workload_of: &workload_of,
        selected: &selected,
        isolation: &isolation,
        admissions: &admissions,
        sign_in: &sign_in,
        controller: &controller,
        signs_in: &signs_in,
        declares: &declares,
    };
    Ways::read(&sources)
}
