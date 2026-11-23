//! The workload elevation shows how a request reaches one workload and
//! where the workload stores its data.  Its main row has a column each
//! for the workload's Ingresses, its Services, its pods, and its
//! volumes, followed by its guards against eviction.  Above the row are
//! links to the namespace's other workloads, and the workload's card is
//! beside it.  Each pod is a tile with its machine, age, restarts, and
//! usage bars.

mod needs;
pub(super) mod sentence;
mod switches;
mod tile_text;
pub(super) mod tiles;

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;
use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::jiff::Timestamp;
use kube::core::ApiResource;

use needs::{Need, needs, secrets};
pub use switches::Switch;
use switches::switches;
use tile_text::claim_pages;
use tiles::{PodTile, State, tiles};

use super::ClusterMap;
use crate::cards::WorkloadCard;
use crate::map::known::{Chip, chips};
use crate::map::pages::Pages;
use transect_cluster::cluster::Custom;
use transect_cluster::ownership::{self, Managed, Ownership};
use transect_cluster::workloads::Workloads;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::{Endpoint, Graph, Kind, Object, Plurals};
use transect_graph::links::{Reference, kinds};
use transect_ground::guards::{Guards, Hold};
use transect_ground::loss::{self, IfDown};
use transect_ground::storage::{self, Mounts, Storage};
use transect_network::addresses::{self, Addresses};
use transect_network::addresses::{Depends, Unread};
use transect_network::entry::{Endpoints, Entry, Issuers, Serving, TrafficResults};
use transect_network::policy::Allowed;
use transect_network::policy::Calls;
use transect_observe::health::{Deciding, Health};
use transect_observe::usage::Usage;
use transect_resources::intent::Intents;
use transect_resources::knowledge::{Definitions, Kinds, KnownThrough, Related, Relation};
use transect_supply::findings;
use transect_supply::forge::Forges;
use transect_supply::gitops::{CodeSection, GitOps};
use transect_supply::images::{Known, Origins};

/// The workload page.  The server renders its `main` block alone as the
/// fragment that replaces the page's content on each pushed update.
#[derive(Template)]
#[template(path = "map/workload/workload.html", blocks = ["main"])]
pub struct WorkloadPage {
    pub namespace: String,
    /// The layer of the cluster map that contains the namespace, which
    /// the breadcrumb shows.
    pub layer: String,
    pub name: String,
    /// The page's path, which `events_url` appends to `/events`.
    pub path: String,
    pub switches: Vec<Switch>,
    /// The Ingresses and Services that reach the workload, in the first
    /// columns of the row.
    pub entry: Entry,
    pub pods_heading: String,
    pub tiles: Vec<PodTile>,
    /// The legend under the tiles, which says what a full usage bar
    /// measures.
    pub legend: String,
    /// Why the bars show no usage, when they show none.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// The claims that the pods mount, in the last columns of the row.
    pub mounts: Mounts,
    /// The page of each mounted claim that has one, by claim name.
    pub claim_pages: HashMap<String, String>,
    /// The addresses in the workload's configuration that point at
    /// something in the cluster, and the number of Secrets that it reads.
    /// Both are shown under the row.
    pub needs: Vec<Need>,
    pub secrets: usize,
    pub guards: Vec<GuardChip>,
    pub card: WorkloadCard,
    /// The custom resources that the workload reconciles as a
    /// controller, with one table for each kind.
    pub manages: Vec<crate::objects::Manages>,
    /// Whether the workload serves a port, and so has an access tab.
    pub access: bool,
}

/// One guard as the page draws it.
#[derive(Debug, PartialEq)]
pub struct GuardChip {
    pub text: String,
    pub title: Option<String>,
    pub hold: Hold,
}

impl GuardChip {
    pub fn class(&self) -> &'static str {
        match self.hold {
            Hold::Held => "guard guard-held",
            Hold::Open => "guard guard-open",
            Hold::Warn => "guard guard-warn",
        }
    }

    pub fn held(&self) -> bool {
        self.hold == Hold::Held
    }
}

/// What the page reads besides the map.
pub struct Contents<'a> {
    /// The objects in the stores that have a page of their own.
    pub with_pages: &'a std::collections::HashSet<Object>,
    pub workloads: &'a Workloads<'a>,
    /// The top owners of the namespace's objects, which are the
    /// workloads that the page can show and switch between.
    pub owners: Vec<&'a Object>,
    /// The ownership index.  Among the Services that a workload's
    /// addresses point at, it separates the workload's own Services from
    /// another workload's.
    pub ownership: &'a Ownership,
    pub plurals: &'a Plurals,
    pub budgets: &'a [Arc<PodDisruptionBudget>],
    pub graph: &'a Graph,
    pub gitops: &'a GitOps,
    pub forges: &'a Forges,
    /// The image labels read so far.
    pub images: &'a Known,
    pub addresses: &'a Addresses,
    pub services: &'a [Arc<Service>],
    pub ingresses: &'a [Arc<Ingress>],
    pub endpoints: &'a Endpoints,
    /// The issuer of each Ingress's certificate.
    pub issuers: &'a Issuers,
    pub storage: &'a Storage,
    pub by_workload: &'a HashMap<Object, Health>,
    pub usage: Option<&'a Usage>,
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// The kinds that transect is not allowed to list.  The card then
    /// says that controllers may be missing, because an owner of an
    /// unlisted kind cannot be found.
    pub rbac_missing: &'a [&'static str],
    /// The callers that the NetworkPolicies allow for each workload.
    pub allowed: &'a Allowed,
    /// The intent declared for each workload.  Its `why` is the first
    /// line of the card.
    pub intents: &'a Intents,
    /// The entries of each workload's intent that refer to an object
    /// that transect cannot read.
    pub unread: &'a HashMap<Object, Vec<Unread>>,
    /// The knowledge of each custom kind.  It says whether a budget that
    /// allows no eviction is intended, and its summary opens the card of
    /// a custom workload with no `why`.
    pub kinds: &'a Kinds,
    /// For each custom resource of a kind with knowledge, the objects
    /// that its fields refer to.
    pub relations: &'a BTreeMap<Object, Vec<Relation>>,
    /// The description from each custom kind's CRD.  It opens the card
    /// of a custom workload that has no `why` and no knowledge summary.
    pub definitions: &'a Definitions,
    /// The objects of each custom kind.  When the kind has no knowledge,
    /// the card of such a workload shows its conditions.
    pub custom: &'a [(ApiResource, Vec<Arc<Custom>>)],
    /// The workload that runs each Ingress's controller.
    pub serving: &'a Serving,
    /// The number of ways in to each workload that serves a port.
    pub ways_in: &'a HashMap<Object, usize>,
    pub vulnerabilities: &'a transect_supply::vulnerabilities::Vulnerabilities,
    /// The ingress controllers' metrics for each backend.
    pub traffic: TrafficResults<'a>,
    pub now: Timestamp,
}

impl WorkloadPage {
    pub fn with_access(self, access: bool) -> Self {
        Self { access, ..self }
    }

    /// The page of the workload `name` among the top owners of
    /// `namespace`, where `resource` is the URL name of its kind.  Returns
    /// `None` when there is no such workload or the map has no such
    /// namespace.
    pub fn build(
        map: &ClusterMap,
        namespace: &str,
        resource: &str,
        name: &str,
        contents: Contents,
    ) -> Option<Self> {
        let layer = map.layer_of(namespace)?.to_owned();
        let plurals = contents.plurals;
        let object = find(&contents.owners, plurals, resource, name)?;
        let path = transect_graph::links::page(object, plurals)?;
        let workload = contents.workloads.get(object);
        let guards = Guards::read(&workload, contents.budgets, contents.services)
            .with_operator_moves(contents.kinds.moves_before_eviction(object));
        let tiles = tiles(workload.pods, contents.usage, contents.now);
        let entry = Entry::served(
            contents.graph,
            object,
            contents.services,
            contents.ingresses,
            contents.endpoints,
            contents.issuers,
            (contents.serving, plurals),
        )
        .with_traffic(contents.traffic);
        let mounts = storage::mounts(
            contents.storage,
            namespace,
            &workload.running(),
            &workload.templates(),
        );
        let templates = workload.templates();
        let dependencies = addresses::of_workload(
            contents.addresses,
            contents.graph,
            contents.ownership,
            object,
            &templates,
        );
        let from = Endpoint::Object(object.clone());
        let depends_on: Vec<_> = contents
            .graph
            .edges_leaving(Kind::DependsOn, &from)
            .collect();
        let reading = tiles.iter().any(|tile| tile.state != State::Finished);
        Some(Self {
            access: false,
            namespace: namespace.into(),
            layer,
            name: name.into(),
            switches: switches(object, &contents),
            entry,
            claim_pages: claim_pages(&mounts, namespace, contents.with_pages, plurals),
            mounts,
            pods_heading: sentence::pods_heading(&tiles),
            legend: sentence::legend(tiles.iter()),
            usage_note: contents.usage_note.clone().filter(|_| reading),
            needs: needs(dependencies, &depends_on, object, plurals),
            secrets: secrets(&templates),
            guards: guard_chips(&guards),
            // The card's title would link to the page it is on, so it has
            // no link here.  The section view's card keeps the link.
            card: card(object, &contents).with_href(None),
            manages: Vec::new(),
            tiles,
            path,
        })
    }

    /// The breadcrumb.  The layer links to the cluster map with this
    /// namespace selected.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
            Crumb::current_name(&self.name),
        ]
    }

    pub fn secrets_text(&self) -> Option<String> {
        sentence::secrets(self.secrets)
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    pub fn tabs(&self) -> Tabs {
        let tabs = Tabs::workload(&self.path, Lens::Overview, self.access);
        match self.card.vulnerabilities.is_some() {
            true => tabs.with_vulnerabilities(&self.path, false),
            false => tabs,
        }
    }
}

/// The card of the workload `object`, which the workload page and the
/// section view each draw beside their main content.  Keep the order of
/// the builder calls, because some read what an earlier call set.
pub fn card(object: &Object, contents: &Contents) -> WorkloadCard {
    let plurals = contents.plurals;
    let workload = contents.workloads.get(object);
    let guards = Guards::read(&workload, contents.budgets, contents.services)
        .with_operator_moves(contents.kinds.moves_before_eviction(object));
    let findings = findings::of_workload(&guards, workload.replicas().unwrap_or(0));
    let health = contents.by_workload.get(object).cloned();
    let unread = contents
        .unread
        .get(object)
        .map(Vec::as_slice)
        .unwrap_or_default();
    let depends = Depends::of_workload(contents.graph, object, plurals, unread, |o| {
        has_page(o, contents)
    });
    let managed = Managed::of_workload(contents.graph, object, plurals, contents.rbac_missing);
    let edges = loss::cascade::Edges {
        graph: contents.graph,
        ingresses: contents.ingresses,
        intents: contents.intents,
    };
    let origins = Origins {
        known: contents.images,
        forges: contents.forges,
    };
    let namespace = object.namespace.as_deref().unwrap_or_default();
    let health = health.unwrap_or_default();
    let known = known_through(object, contents);
    let v = contents.vulnerabilities;
    WorkloadCard::build(&workload, health, findings, depends, contents.now)
        .with_origins(origins.of_pods(workload.pods.iter().copied()))
        .with_known(known)
        .with_href(contents.pages().href(object))
        .with_access(contents.ways_in.get(object).copied())
        .with_vulnerabilities(v.names(object), v.count_line(object))
        .with_reason_pages(|reason| {
            reason.page(namespace, contents.ownership, object, |shown| {
                contents.pages().href(shown)
            })
        })
        .with_intent(contents.intents.of(object), contents.definitions.of(object))
        .with_managed(managed)
        .with_if_down(
            IfDown::of(&edges, object)
                .with_plurals(contents.plurals)
                .linked(|o| contents.pages().href(o)),
        )
        .with_calls(Calls::of_workload(contents.allowed, object, plurals))
        .with_conditions(conditions(object, contents))
        .with_code(
            CodeSection::of_workload(
                contents.graph,
                contents.gitops,
                contents.forges,
                object,
                namespace,
            )
            .linked(|o| contents.pages().href(o)),
        )
}

/// The conditions that the custom workload `object` reports, when its
/// kind has no knowledge.  A kind with knowledge shows its conditions on
/// its board, so this returns none for it.
fn conditions(object: &Object, contents: &Contents) -> Vec<Chip> {
    if !ownership::custom(object) || contents.kinds.of_object(object).is_some() {
        return Vec::new();
    }
    let of_kind = contents
        .custom
        .iter()
        .filter(|(resource, _)| resource.group == object.group && resource.kind == object.kind);
    let mut found = of_kind.flat_map(|(_, objects)| objects).filter(|custom| {
        let meta = &custom.metadata;
        meta.name.as_deref() == Some(&object.name) && meta.namespace == object.namespace
    });
    found
        .next()
        .map(|custom| chips(&custom.data, &Deciding::of(contents.kinds, object)))
        .unwrap_or_default()
}

/// The knowledge of `object`'s kind, with the objects that its fields
/// refer to and its controller, or `None` when the kind has no
/// knowledge.
pub fn known_through(object: &Object, contents: &Contents) -> Option<KnownThrough> {
    let knowledge = contents.kinds.of_object(object)?;
    let relations = contents.relations.get(object).into_iter().flatten();
    let related = relations.map(|relation| {
        let namespace = object.namespace.as_deref();
        Related::of(relation, namespace, contents.plurals, |o| {
            has_page(o, contents)
        })
    });
    let controller = controller(contents.graph, object).map(|controller| Reference {
        href: contents.pages().href(&controller),
        text: controller.name,
    });
    Some(KnownThrough::of(knowledge, controller).with_related(related.collect()))
}

pub fn has_page(object: &Object, contents: &Contents) -> bool {
    contents.pages().has(object)
}

impl Contents<'_> {
    pub fn pages(&self) -> Pages<'_> {
        Pages {
            with_pages: self.with_pages,
            ownership: self.ownership,
            relations: self.relations,
            gitops: self.gitops,
            plurals: self.plurals,
        }
    }
}

/// The workload that controls the custom resource `object`, when the
/// graph has exactly one `ControlledBy` edge from it.  Returns `None`
/// when it has none or several.
pub fn controller(graph: &Graph, object: &Object) -> Option<Object> {
    let from = Endpoint::Object(object.clone());
    let edges = graph
        .of_kind(Kind::ControlledBy)
        .filter(|edge| edge.from == from);
    let controllers: Vec<&Object> = edges
        .filter_map(|edge| match &edge.to {
            Endpoint::Object(controller) => Some(controller),
            _ => None,
        })
        .collect();
    match controllers[..] {
        [one] => Some(one.clone()),
        _ => None,
    }
}

/// The workload in `owners` named `name` whose kind has the URL name
/// `resource`.
pub fn find<'a>(
    owners: &[&'a Object],
    plurals: &Plurals,
    resource: &str,
    name: &str,
) -> Option<&'a Object> {
    let found = owners.iter().find(|owner| {
        owner.name == name && kinds::workload(owner, plurals).as_deref() == Some(resource)
    });
    found.copied()
}

fn guard_chips(guards: &Guards) -> Vec<GuardChip> {
    let list = guards.list();
    list.iter()
        .map(|guard| GuardChip {
            text: guard.to_string(),
            title: guard.title(),
            hold: guards.hold(guard),
        })
        .collect()
}
