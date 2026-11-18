use rstest::rstest;

use super::fixtures::outside;
use super::*;
use crate::storage::fixtures::{claim, csi, nfs};
use transect_graph::edges::{Kind, Object};

fn outsides() -> Outsides {
    Outsides::new(&[outside(
        "a-nas",
        "{title: The NAS, addresses: [NAS.example.com], drivers: [csi.example.com], why: Holds every volume that must outlive a machine.}",
    )])
}

#[rstest]
#[case::an_address("nas.example.com", "The NAS")]
#[case::an_address_in_capitals("Nas.Example.Com", "The NAS")]
#[case::a_driver("csi.example.com", "The NAS")]
#[case::another_backend("backups.example.com", "backups.example.com")]
fn names_each_backend_by_its_box(#[case] backend: &str, #[case] name: &str) {
    assert_eq!(outsides().box_of(backend), name);
}

#[rstest]
fn folds_an_appliances_backends_into_one_box_and_sums_its_volumes() {
    let volumes = [
        nfs("pv-media", "nas.example.com"),
        csi("pv-database", "csi.example.com", "fast"),
        nfs("pv-backups", "backups.example.com"),
    ];
    let claims = [
        claim("shop", "media", Some("pv-media")),
        claim("shop", "database", Some("pv-database")),
        claim("shop", "backups", Some("pv-backups")),
    ];
    let storage = outsides().fold_storage(Storage::build(&claims, &volumes, &[]));
    let tally: Vec<(&str, usize)> = storage
        .tally(None)
        .into_iter()
        .map(|(name, kinds)| (name, kinds.values().sum()))
        .collect();
    assert_eq!(tally, vec![("The NAS", 2), ("backups.example.com", 1)]);
}

#[rstest]
fn ends_a_dependency_on_any_of_its_addresses_at_the_box() {
    let web = Endpoint::Object(Object::new("apps", "Deployment", Some("shop"), "web"));
    let edge = |to: &str| Edge {
        kind: Kind::DependsOn,
        from: web.clone(),
        to: Endpoint::Outside(to.into()),
        guessed: false,
        provenance: Provenance::Kubernetes,
    };
    let folded: Vec<Endpoint> = outsides()
        .fold(vec![edge("nas.example.com"), edge("api.example.com")])
        .into_iter()
        .map(|edge| edge.to)
        .collect();
    assert_eq!(
        folded,
        vec![
            Endpoint::Outside("The NAS".into()),
            Endpoint::Outside("api.example.com".into())
        ]
    );
}

#[rstest]
fn the_first_outside_by_name_owns_a_backend_that_two_list() {
    let found = Outsides::new(&[
        outside("zeta", "{title: Zeta, drivers: [csi.example.com]}"),
        outside("alpha", "{title: Alpha, drivers: [csi.example.com]}"),
    ]);
    let messages: Vec<&str> = found
        .conditions
        .iter()
        .map(|(_, c)| c.message.as_str())
        .collect();
    let message = "csi.example.com is listed by alpha and zeta, and alpha draws it because its name sorts first";
    assert_eq!(
        (found.box_of("csi.example.com"), messages),
        ("Alpha", vec![message, message])
    );
}

#[rstest]
fn marks_an_outsides_text_as_its_stated_intent() {
    let lines = outsides().of("nas.example.com").unwrap().lines();
    let provenance = Provenance::Intent(Stated::Outside {
        field: "why".into(),
        name: "a-nas".into(),
    });
    assert_eq!(
        lines,
        vec![Line {
            text: "Holds every volume that must outlive a machine.".into(),
            provenance
        }]
    );
}

#[rstest]
#[case::https("https://nas.example.com:5001", Some("https://nas.example.com:5001"))]
#[case::http_in_capitals("HTTP://nas.example.com", Some("HTTP://nas.example.com"))]
#[case::a_script("javascript:alert(1)", None)]
#[case::a_file("file:///etc/passwd", None)]
#[case::no_scheme("nas.example.com", None)]
fn links_out_only_to_a_plain_web_url(#[case] url: &str, #[case] link: Option<&str>) {
    let found = outside(
        "a-nas",
        &format!("{{title: The NAS, drivers: [csi.example.com], url: '{url}'}}"),
    );
    assert_eq!((found.url.as_deref(), found.link()), (Some(url), link));
}

#[rstest]
#[case::an_outsides_box("The NAS", "/ground/a-nas", None)]
#[case::an_address_it_lists("nas.example.com", "/ground/a-nas", None)]
#[case::a_driver_it_lists("csi.example.com", "/ground/a-nas", None)]
#[case::a_bare_backend("backups.example.com", "/ground/backups.example.com", None)]
fn pages_each_box(#[case] title: &str, #[case] page: &str, #[case] link: Option<String>) {
    let found = outsides();
    assert_eq!(
        (found.page_of(title), found.link_of(title)),
        (page.to_string(), link)
    );
}

#[rstest]
fn lists_the_claims_a_box_keeps_by_type() {
    let volumes = [
        nfs("pv-media", "nas.example.com"),
        csi("pv-database", "csi.example.com", "fast"),
        nfs("pv-backups", "backups.example.com"),
    ];
    let claims = [
        claim("shop", "media", Some("pv-media")),
        claim("blog", "database", Some("pv-database")),
        claim("shop", "backups", Some("pv-backups")),
    ];
    let storage = outsides().fold_storage(Storage::build(&claims, &volumes, &[]));
    let on: Vec<(String, String)> = storage
        .claims_on("The NAS")
        .into_iter()
        .map(|(claim, kind)| (format!("{}/{}", claim.namespace, claim.name), kind.label()))
        .collect();
    assert_eq!(
        on,
        vec![
            ("shop/media".to_string(), "NFS".to_string()),
            ("blog/database".to_string(), "fast".to_string()),
        ]
    );
}

/// The outside system "The NAS", which uses the driver `csi.example.com`,
/// with `url` when one is given, and with each of `consoles` as a console
/// URL filled from that driver's template.
fn with_consoles(url: Option<&str>, consoles: &[&str]) -> Outsides {
    let url = url.map(|url| format!(", url: '{url}'")).unwrap_or_default();
    let nas = outside(
        "a-nas",
        &format!("{{title: The NAS, drivers: [csi.example.com]{url}}}"),
    );
    let consoles = consoles.iter().map(|url| url.to_string()).collect();
    Outsides::new(&[nas]).with_consoles(HashMap::from([("csi.example.com".to_string(), consoles)]))
}

#[rstest]
#[case::one_console(None, &["https://nas.example.com:5001"], Some("https://nas.example.com:5001"), 1)]
#[case::several_link_none(None, &["https://a:5001", "https://b:5001"], None, 2)]
#[case::the_outsides_url_wins(Some("https://nas.example.com"), &["https://a:5001"], Some("https://nas.example.com"), 0)]
#[case::an_outsides_url_that_is_no_link(Some("nas.example.com"), &["https://a:5001"], None, 0)]
fn links_to_the_console_its_drivers_template_fills(
    #[case] url: Option<&str>,
    #[case] consoles: &[&str],
    #[case] link: Option<&str>,
    #[case] listed: usize,
) {
    let found = with_consoles(url, consoles);
    assert_eq!(
        (
            found.link_of("The NAS").as_deref(),
            found.consoles_of("The NAS").len()
        ),
        (link, listed)
    );
}
