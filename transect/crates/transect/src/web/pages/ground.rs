//! A ground box's page, read from the snapshot: the `Outside` its name
//! names, or the backend of that name, with the claims it keeps, the
//! workloads whose volumes are on it, and what reaches it.
//!
//! The page and every link to it read the same lookup, `gathered`, so
//! a link to a ground box opens a page.

use std::collections::BTreeSet;

use super::super::model::Model;
use transect_graph::edges::Claim;
use transect_graph::edges::{Endpoint, Kind, Object, Shown};
use transect_ground::outside::Outside;
use transect_ground::storage::{self, Type};
use transect_map::map::ground::{Ground, GroundPage};

/// What the snapshot holds of the ground box `name` names.
struct Gathered<'a> {
    outside: Option<&'a Outside>,
    /// Its `Outside`'s name, or the backend's.
    name: &'a str,
    title: &'a str,
    claims: Vec<(&'a Claim, &'a Type)>,
    stores: Vec<Object>,
    dependents: Vec<Object>,
}

/// The ground box `name` names: an `Outside` by its name, by an address
/// or driver it lists, or by its title, or a backend no `Outside` lists
/// that a claim, a volume, or an edge of any kind reaches.  Each name of
/// one `Outside` gathers the same box.  `None` when nothing outside the
/// cluster goes by that name.
fn gathered<'a>(model: &'a Model, name: &'a str) -> Option<Gathered<'a>> {
    let snapshot = &model.snapshot;
    let outsides = &snapshot.outsides;
    let outside = outsides.resolve(name);
    let name = outside.map_or(name, |outside| outside.name.as_str());
    let title = outside.map_or(name, |outside| outside.title.as_str());
    let claims = snapshot.storage.claims_on(title);
    let graph = model.graph(&Shown::parse(None));
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

/// The page of the ground box `name` names, or `None` when nothing
/// outside the cluster goes by that name.
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
    // The page reads every edge kind whatever the URL turns off, so it
    // shows no edge kinds to turn off.
    Some(page)
}

/// The link to the ground box `name` names, when it has a page.
pub fn ground_link(model: &Model, name: &str) -> Option<String> {
    gathered(model, name).map(|gathered| model.snapshot.outsides.page_of(gathered.name))
}
