//! A ground box's own page, `/ground/{name}`, which shows the thing
//! outside the cluster as a board.  The box at the top gives its title,
//! its link out to its own console, and the lines its Outside gives
//! about it.  Boxes beside it list the addresses and drivers that reach
//! it, the claims it keeps by type, the workloads whose volumes are on
//! it, and every workload configured to reach it.  A backend that no
//! Outside names, such as an NFS server's address, gets the same page
//! by its own name, with only what the API reports.

use std::collections::{BTreeMap, BTreeSet};

use askama::Template;

use crate::objects::{Section, Side};
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Claim, Object};
use transect_graph::links::Reference;
use transect_ground::outside::Outside;
use transect_ground::storage::Type;
use transect_resources::intent::Line;

#[derive(Template)]
#[template(path = "map/ground/ground.html", blocks = ["main"])]
pub struct GroundPage {
    /// The name in its URL: its Outside's name, or the backend's.
    pub name: String,
    /// What its box shows, such as "The NAS".
    pub title: String,
    /// Its own console, when its Outside gives a URL a browser may open,
    /// or its drivers' console template fills one.
    pub link: Option<String>,
    /// Each console its drivers' template fills, when it fills more
    /// than one.  The title then links to none of them.
    pub consoles: Vec<String>,
    /// Its Outside's `url` as text, when it is not one to open.
    pub url_text: Option<String>,
    pub about: Vec<Line>,
    /// How it is reached, on the board's left.
    pub reached: Section,
    /// What it keeps and what uses it, on the board's right.
    pub uses: Vec<Section>,
    /// "2 volumes · 3 workloads use it".
    pub summary: String,
}

/// What a ground page reads.
pub struct Ground<'a> {
    /// The box's title, as the ground band shows it.
    pub title: &'a str,
    pub outside: Option<&'a Outside>,
    pub claims: Vec<(&'a Claim, &'a Type)>,
    /// The workloads whose volumes are on it: through a claim, or an
    /// NFS volume a pod names by one of its addresses.
    pub stores: Vec<&'a Object>,
    /// The objects whose edges of any kind except stores-on reach it,
    /// each configured with its address.
    pub dependents: Vec<&'a Object>,
    /// The console URLs its drivers' `console` template fills, when its
    /// Outside gives no `url`.
    pub consoles: Vec<String>,
}

impl GroundPage {
    /// The page `ground` draws, under the name `name`, with `href` giving
    /// each object's page.
    pub fn build(name: &str, ground: Ground, href: impl Fn(&Object) -> Option<String>) -> Self {
        let outside = ground.outside;
        let reached = Section::new("Reached as", Side::BelongsTo)
            .literals(
                "addresses",
                outside.map(|o| o.addresses.clone()).unwrap_or_default(),
            )
            .literals(
                "drivers",
                outside.map(|o| o.drivers.clone()).unwrap_or_default(),
            )
            .or("No Outside names it, so the map has only this name for it.");
        let mut volumes = Section::new("Volumes it keeps", Side::Out);
        let mut kinds: Vec<&Type> = ground.claims.iter().map(|(_, kind)| *kind).collect();
        kinds.dedup();
        for kind in kinds {
            let claims = ground.claims.iter().filter(|(_, of)| *of == kind);
            let claims = claims.map(|(claim, _)| {
                let object = Object::new(
                    "",
                    "PersistentVolumeClaim",
                    Some(&claim.namespace),
                    &claim.name,
                );
                Reference {
                    text: format!("{}/{}", claim.namespace, claim.name),
                    href: href(&object),
                }
            });
            volumes = volumes.fact(&kind.label(), claims.collect());
        }
        let volumes = volumes.or("No volume of the cluster's is on it.");
        let reference = |object: &&Object| Reference {
            text: transect_graph::edges::kind_and_name(&object.kind, &object.name),
            href: href(object),
        };
        let grouped = |title: &str, objects: &[&Object], none: &str| {
            let namespaces = by_namespace(objects);
            let summary = folded(objects.len(), namespaces.len());
            let mut section = Section::new(title, Side::Into);
            for (namespace, objects) in namespaces {
                section = section.fact(namespace, objects.iter().map(reference).collect());
            }
            let section = section.or(none);
            match summary {
                Some(summary) => section.folded(summary),
                None => section,
            }
        };
        let stores = grouped(
            "Keeps the data of",
            &ground.stores,
            "No workload's volumes are on it.",
        );
        let depended = grouped(
            "Configured to reach it",
            &ground.dependents,
            "No workload is configured with its address.",
        );
        let used = used(&ground.stores, &ground.dependents);
        Self {
            name: name.into(),
            title: ground.title.into(),
            link: match (outside.and_then(Outside::link), ground.consoles.as_slice()) {
                (Some(link), _) => Some(link.to_string()),
                (None, [one]) => Some(one.clone()),
                _ => None,
            },
            consoles: match ground.consoles.len() {
                0 | 1 => Vec::new(),
                _ => ground.consoles.clone(),
            },
            url_text: outside
                .filter(|outside| outside.link().is_none())
                .and_then(|outside| outside.url.clone()),
            about: outside.map(Outside::lines).unwrap_or_default(),
            summary: summary(ground.claims.len(), used),
            reached,
            uses: vec![volumes, stores, depended],
        }
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::current("ground"),
            Crumb::current_name(&self.title),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events/ground/{}", self.name)
    }

    /// What the marks on the page mean, when any line is marked.
    pub fn legend(&self) -> Option<String> {
        transect_graph::edges::provenance::legend(self.about.iter().map(|line| &line.provenance))
    }
}

/// `objects` by namespace, each namespace once and in order.
fn by_namespace<'a>(objects: &[&'a Object]) -> BTreeMap<&'a str, Vec<&'a Object>> {
    let mut found: BTreeMap<&str, Vec<&Object>> = BTreeMap::new();
    for object in objects {
        let namespace = object.namespace.as_deref().unwrap_or("cluster");
        found.entry(namespace).or_default().push(object);
    }
    found
}

/// The most workloads a box of them lists before it folds.
const UNFOLDED: usize = 12;

/// "128 workloads in 40 namespaces", for a list of workloads too long
/// to show unfolded, or none for a short one.
fn folded(workloads: usize, namespaces: usize) -> Option<String> {
    let plural = |n: usize| if n == 1 { "" } else { "s" };
    (workloads > UNFOLDED).then(|| {
        format!(
            "{workloads} workload{} in {namespaces} namespace{}",
            plural(workloads),
            plural(namespaces)
        )
    })
}

/// How many workloads use it, each once whether it stores on it, is
/// configured to reach it, or both.
fn used(stores: &[&Object], dependents: &[&Object]) -> usize {
    let all: BTreeSet<&Object> = stores.iter().chain(dependents).copied().collect();
    all.len()
}

/// "2 volumes · 3 workloads use it", or "Nothing in the cluster uses
/// it." when neither.
fn summary(volumes: usize, workloads: usize) -> String {
    let volumes = match volumes {
        0 => None,
        1 => Some("1 volume".to_string()),
        count => Some(format!("{count} volumes")),
    };
    let workloads = match workloads {
        0 => None,
        1 => Some("1 workload uses it".to_string()),
        count => Some(format!("{count} workloads use it")),
    };
    let parts: Vec<String> = volumes.into_iter().chain(workloads).collect();
    match parts.is_empty() {
        true => "Nothing in the cluster uses it.".into(),
        false => parts.join(" · "),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_ground::outside::fixtures::outside;

    #[rstest]
    #[case::short(12, 3, None)]
    #[case::long(30, 1, Some("30 workloads in 1 namespace"))]
    fn folds_a_long_list_of_workloads(
        #[case] workloads: usize,
        #[case] namespaces: usize,
        #[case] summary: Option<&str>,
    ) {
        assert_eq!(folded(workloads, namespaces).as_deref(), summary);
    }

    #[rstest]
    #[case::neither(0, 0, "Nothing in the cluster uses it.")]
    #[case::volumes(2, 0, "2 volumes")]
    #[case::both(1, 3, "1 volume · 3 workloads use it")]
    #[case::one_workload(0, 1, "1 workload uses it")]
    fn sums_up_what_uses_it(#[case] volumes: usize, #[case] workloads: usize, #[case] text: &str) {
        assert_eq!(summary(volumes, workloads), text);
    }

    fn page_of(spec: &str, stores: &[&Object], dependents: &[&Object]) -> GroundPage {
        let nas = outside("a-nas", spec);
        let claim = Claim::new("shop", "media");
        let ground = Ground {
            title: "The NAS",
            outside: Some(&nas),
            claims: vec![(&claim, &Type::Nfs)],
            stores: stores.to_vec(),
            dependents: dependents.to_vec(),
            consoles: Vec::new(),
        };
        GroundPage::build("a-nas", ground, |object| Some(format!("/{}", object.name)))
    }

    fn deployment(namespace: &str, name: &str) -> Object {
        Object::new("apps", "Deployment", Some(namespace), name)
    }

    fn page(spec: &str) -> GroundPage {
        let (web, db) = (deployment("shop", "web"), deployment("shop", "db"));
        page_of(spec, &[&db], &[&web])
    }

    /// A fact's label and the text of each of its values.
    type Read = (String, Vec<String>);

    /// Each box's title and its facts.
    fn read(page: &GroundPage) -> Vec<(String, Vec<Read>)> {
        let section = |s: &Section| {
            let facts = s.facts.iter().map(|fact| {
                let values = fact.values.iter().map(|v| v.text.clone()).collect();
                (fact.label.clone(), values)
            });
            (s.title.clone(), facts.collect())
        };
        let sections = std::iter::once(&page.reached).chain(&page.uses);
        sections.map(section).collect()
    }

    #[rstest]
    fn boxes_how_it_is_reached_what_it_keeps_and_who_needs_it() {
        let page =
            page("{title: The NAS, addresses: [nas.example.com], drivers: [csi.example.com]}");
        let strings = |values: &[&str]| values.iter().map(|v| v.to_string()).collect::<Vec<_>>();
        assert_eq!(
            read(&page),
            vec![
                (
                    "Reached as".into(),
                    vec![
                        ("addresses".into(), strings(&["nas.example.com"])),
                        ("drivers".into(), strings(&["csi.example.com"])),
                    ]
                ),
                (
                    "Volumes it keeps".into(),
                    vec![("NFS".into(), strings(&["shop/media"]))]
                ),
                (
                    "Keeps the data of".into(),
                    vec![("shop".into(), strings(&["Deployment db"]))]
                ),
                (
                    "Configured to reach it".into(),
                    vec![("shop".into(), strings(&["Deployment web"]))]
                ),
            ]
        );
    }

    #[rstest]
    fn counts_the_workloads_whose_volumes_are_on_it_with_no_address_dependent() {
        let (web, db, jobs) = (
            deployment("shop", "web"),
            deployment("shop", "db"),
            deployment("batch", "jobs"),
        );
        let page = page_of(
            "{title: The NAS, drivers: [csi.example.com]}",
            &[&web, &db, &jobs],
            &[],
        );
        let stores = &page.uses[1];
        let facts: Vec<(&str, usize)> = stores
            .facts
            .iter()
            .map(|fact| (fact.label.as_str(), fact.values.len()))
            .collect();
        assert_eq!(
            (page.summary.as_str(), facts),
            (
                "1 volume · 3 workloads use it",
                vec![("batch", 1), ("shop", 2)]
            )
        );
    }

    #[rstest]
    #[case::a_web_console("https://nas.example.com", Some("https://nas.example.com"), None)]
    #[case::another_scheme("smb://nas.example.com", None, Some("smb://nas.example.com"))]
    fn links_out_only_to_a_web_console(
        #[case] url: &str,
        #[case] link: Option<&str>,
        #[case] text: Option<&str>,
    ) {
        let page = page(&format!(
            "{{title: The NAS, drivers: [csi.example.com], url: '{url}'}}"
        ));
        assert_eq!(
            (page.link.as_deref(), page.url_text.as_deref()),
            (link, text)
        );
    }
}
