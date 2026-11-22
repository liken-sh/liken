//! The page at `/ground/{name}` for a system outside the cluster, such
//! as an NFS server, that workloads store data on or depend on. The
//! outside-systems band of the cluster map has one box for each such system. The
//! page shows the `Outside` that declares the system, when one does,
//! the claims on it, the workloads whose volumes are on it, and the
//! objects that depend on it.
//!
//! `ground_page` and `ground_link` both call `gathered`, so a link
//! exists exactly when the page does.

use std::collections::BTreeSet;

use super::super::model::Model;
use transect_graph::edges::Claim;
use transect_graph::edges::{Endpoint, Kind, Object};
use transect_ground::outside::Outside;
use transect_ground::storage::{self, Type};
use transect_map::map::ground::{Ground, GroundPage};

/// What the snapshot holds about one outside system.
struct Gathered<'a> {
    outside: Option<&'a Outside>,
    /// The name of the `Outside`, or the backend's name when no
    /// `Outside` lists it.
    name: &'a str,
    title: &'a str,
    claims: Vec<(&'a Claim, &'a Type)>,
    stores: Vec<Object>,
    dependents: Vec<Object>,
}

/// Collects what the snapshot holds about the outside system `name`.
/// `name` can be the name or the title of an `Outside`, or an address
/// or a driver that an `Outside` lists, and each of these finds the same
/// system. It can also be a backend that no `Outside` lists but that a
/// claim, a pod's inline NFS volume, or an edge refers to. Returns
/// `None` when nothing in the snapshot refers to `name`.
fn gathered<'a>(model: &'a Model, name: &'a str) -> Option<Gathered<'a>> {
    let snapshot = &model.snapshot;
    let outsides = &snapshot.outsides;
    let outside = outsides.resolve(name);
    let name = outside.map_or(name, |outside| outside.name.as_str());
    let title = outside.map_or(name, |outside| outside.title.as_str());
    let claims = snapshot.storage.claims_on(title);
    // The page lists edges of every kind, whatever kinds the URL turns
    // off.
    let graph = model.graph();
    let end = Endpoint::Outside(title.to_string());
    let reaching = |stores: bool| -> BTreeSet<Object> {
        let edges = graph.every_edge_reaching(&end);
        let edges = edges.filter(|edge| (edge.kind == Kind::StoresOn) == stores);
        let froms = edges.filter_map(|edge| match &edge.from {
            Endpoint::Object(object) => Some(object.clone()),
            _ => None,
        });
        froms.collect()
    };
    let mut stores = reaching(true);
    let pods = snapshot.pods.iter().map(AsRef::as_ref);
    let inline = storage::inline_nfs(pods, &snapshot.ownership);
    let inline = inline
        .into_iter()
        .filter(|(_, server)| outsides.box_of(server) == title);
    stores.extend(inline.map(|(owner, _)| owner));
    let dependents = reaching(false);
    if outside.is_none() && claims.is_empty() && stores.is_empty() && dependents.is_empty() {
        return None;
    }
    Some(Gathered {
        outside,
        name,
        title,
        claims,
        stores: stores.into_iter().collect(),
        dependents: dependents.into_iter().collect(),
    })
}

/// The page of the outside system `name`, or `None` when nothing in
/// the snapshot refers to `name`.
pub fn ground_page(model: &Model, name: &str) -> Option<GroundPage> {
    let gathered = gathered(model, name)?;
    let snapshot = &model.snapshot;
    let pages = snapshot.pages();
    let ground = Ground {
        title: gathered.title,
        outside: gathered.outside,
        claims: gathered.claims,
        stores: gathered.stores.iter().collect(),
        dependents: gathered.dependents.iter().collect(),
        consoles: snapshot.outsides.consoles_of(gathered.name),
    };
    let page = GroundPage::build(gathered.name, ground, |object| pages.href(object));
    Some(page)
}

/// The link to the page of the outside system `name`, or `None` when
/// it has no page.
pub fn ground_link(model: &Model, name: &str) -> Option<String> {
    gathered(model, name).map(|gathered| model.snapshot.outsides.page_of(gathered.name))
}
