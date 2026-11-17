//! The workload elevation shows how a request reaches one workload and
//! where it stores its data, as a row of columns from its Ingresses to
//! its Services to its pods to its volumes, then the guards that keep it
//! available.  A row of the namespace's other workloads to switch between
//! is above, and the workload's card is beside it.  Each pod is a tile
//! with its machine, age, restarts, and usage bars.

mod needs;
pub(super) mod sentence;
mod tile_text;
pub(super) mod tiles;

use std::collections::HashMap;
use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;
use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::jiff::Timestamp;
use kube::core::ApiResource;

use needs::{Need, needs, secrets};
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
use transect_network::entry::{Answers, Endpoints, Entry, Issuers, Serving};
use transect_network::policy::Allowed;
use transect_network::policy::Calls;
use transect_observe::health::{Deciding, Health, Level};
use transect_observe::usage::Usage;
use transect_resources::intent::Intents;
use transect_resources::knowledge::{Definitions, Kinds, KnownThrough, Related, Relation};
use transect_supply::findings;
use transect_supply::forge::Forges;
use transect_supply::gitops::{CodeSection, GitOps};
use transect_supply::images::{Known, Origins};

/// The workload page.  Its `main` block renders alone as the fragment
/// a pushed update replaces, as every elevation's does.
#[derive(Template)]
#[template(path = "map/workload/workload.html", blocks = ["main"])]
pub struct WorkloadPage {
    /// The main column's row of edge kinds, which the page function sets.
    pub edge_row: super::EdgeRow,
    pub namespace: String,
    /// The band that contains the namespace, which the crumb names.
    pub layer: String,
    pub name: String,
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    pub switches: Vec<Switch>,
    /// The Ingresses and Services that reach it, which start the row.
    pub entry: Entry,
    pub pods_heading: String,
    pub tiles: Vec<PodTile>,
    /// The legend under the tiles, which gives what a full bar measures.
    pub legend: String,
    /// Why the bars show no usage, when they show none.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// The claims its pods mount, which end the row.
    pub mounts: Mounts,
    /// The page of each mounted claim the cluster has, by its name.
    pub claim_pages: HashMap<String, String>,
    /// The addresses it is configured with that reach something in the
    /// cluster, and how many Secrets it reads, under the row.
    pub needs: Vec<Need>,
    pub secrets: usize,
    pub guards: Vec<GuardChip>,
    pub card: WorkloadCard,
    /// The custom resources its controller reconciles, one table a kind.
    pub manages: Vec<crate::objects::Manages>,
    /// Whether it serves a port, and so has an access tab.
    pub access: bool,
}

/// One workload in the row to switch between.
#[derive(Debug, PartialEq)]
pub struct Switch {
    /// Its name, and "×3" after it when it asks for other than one pod.
    pub label: String,
    pub href: String,
    /// The workload this page shows.
    pub current: bool,
    pub level: Level,
}

impl Switch {
    /// The current workload gets the selection's ring, as a selected
    /// parcel does, and one that needs attention gets --warm.
    pub fn class(&self) -> String {
        let mut class = String::from("switch");
        if self.current {
            class.push_str(" switch-current");
        }
        if self.level >= Level::Warning {
            class.push_str(" switch-warning");
        }
        class
    }
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
    /// Every object in a store that has a page.
    pub held: &'a std::collections::HashSet<Object>,
    pub workloads: &'a Workloads<'a>,
    /// The namespace's top owners.
    pub owners: Vec<&'a Object>,
    /// The ownership index, which separates a workload's own Services
    /// from another's among the Services its addresses reach.
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
    /// The RBAC kinds transect may not list, which keep attribution from
    /// finding some controllers.
    pub rbac_missing: &'a [&'static str],
    /// Who the NetworkPolicies let call whom, which the card lists.
    pub allowed: &'a Allowed,
    /// The intent for each workload, which starts its card.
    pub intents: &'a Intents,
    /// The entries of intent that name nothing readable, by workload.
    pub unread: &'a HashMap<Object, Vec<Unread>>,
    /// Kind knowledge of the custom kinds, which shows when a budget that
    /// allows no eviction is by design, and starts a custom kind's card.
    pub kinds: &'a Kinds,
    /// What each custom resource of a known kind names, which its card
    /// lists under its kind's knowledge.
    pub relations: &'a HashMap<Object, Vec<Relation>>,
    /// The description from each custom kind's CRD, which starts the card
    /// of a custom workload that has no intent and no knowledge.
    pub definitions: &'a Definitions,
    /// The custom kinds' objects, whose conditions a custom workload's
    /// card shows when its kind has no knowledge.
    pub custom: &'a [(ApiResource, Vec<Arc<Custom>>)],
    /// What finds the workload that runs each Ingress's controller.
    pub serving: &'a Serving,
    /// How many ways in reach each workload that serves a port.
    pub ways_in: &'a HashMap<Object, usize>,
    pub vulnerabilities: &'a transect_supply::vulnerabilities::Vulnerabilities,
    /// What the ingress controllers' metrics report for each backend.
    pub traffic: Answers<'a>,
    pub now: Timestamp,
}

impl WorkloadPage {
    pub fn with_access(self, access: bool) -> Self {
        Self { access, ..self }
    }

    /// The page with the main column's row of edge kinds.
    pub fn with_edge_row(self, edge_row: super::EdgeRow) -> Self {
        Self { edge_row, ..self }
    }

    /// The page for the workload named `name` whose kind `resource`
    /// names, among `namespace`'s top owners, or `None` when it holds no
    /// such workload or `map` no such namespace.
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
            edge_row: super::EdgeRow::default(),
            namespace: namespace.into(),
            layer,
            name: name.into(),
            switches: switches(object, &contents),
            entry,
            claim_pages: claim_pages(&mounts, namespace, contents.held, plurals),
            mounts,
            pods_heading: sentence::pods_heading(&tiles),
            legend: sentence::legend(tiles.iter()),
            usage_note: contents.usage_note.clone().filter(|_| reading),
            needs: needs(dependencies, &depends_on, object, plurals),
            secrets: secrets(&templates),
            guards: guard_chips(&guards),
            // The card is on the workload's own page, so its title is not
            // a link.  The section view's card keeps the link.
            card: card(object, &contents).with_href(None),
            manages: Vec::new(),
            tiles,
            path,
        })
    }

    /// The crumb: the cluster links home, the layer back to the cluster
    /// map with this namespace selected, and the namespace to its page.
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

/// The card of the workload `object`, which its page and its section draw
/// beside them.
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

/// The conditions `object` reports, for a custom workload whose kind
/// has no knowledge: a `Ready` condition decides its health then, so it
/// comes first.  A kind with knowledge shows its conditions on its board.
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

/// What `object`'s kind's knowledge says about it, with what each of
/// its fields names, or none when its kind has no knowledge.
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

/// Whether `object` has a page of its own, as `map::pages::Pages` decides.
pub fn has_page(object: &Object, contents: &Contents) -> bool {
    contents.pages().has(object)
}

impl Contents<'_> {
    pub fn pages(&self) -> Pages<'_> {
        Pages {
            held: self.held,
            ownership: self.ownership,
            relations: self.relations,
            gitops: self.gitops,
            plurals: self.plurals,
        }
    }
}

/// The one workload that controls `object`, or none when attribution
/// found none or found several and could not choose between them.
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

/// The workload of `owners` named `name` whose kind `resource` names.
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

/// The namespace's workloads that have a page, built-in kinds first and
/// each group by name, so a workload keeps its place in the row as the
/// reader switches.
fn switches(current: &Object, contents: &Contents) -> Vec<Switch> {
    let mut switches: Vec<(bool, &str, Switch)> = contents
        .owners
        .iter()
        .filter_map(|&owner| {
            let href = transect_graph::links::page(owner, contents.plurals)?;
            let replicas = contents.workloads.get(owner).replicas();
            let label = match replicas {
                Some(replicas) if replicas != 1 => format!("{} ×{replicas}", owner.name),
                _ => owner.name.clone(),
            };
            let custom = ownership::custom(owner);
            let level = contents.by_workload.get(owner).map(|h| h.level);
            let switch = Switch {
                label,
                href,
                current: owner == current,
                level: level.unwrap_or_default(),
            };
            Some((custom, owner.name.as_str(), switch))
        })
        .collect();
    switches.sort_by(|a, b| (a.0, a.1).cmp(&(b.0, b.1)));
    switches.into_iter().map(|(_, _, switch)| switch).collect()
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
