//! What an object's page and a kind's page draw from the stores, and
//! what a page of a kind no store holds draws from its one read.

use std::sync::Arc;

use rstest::rstest;

use super::super::super::fixture;
use super::super::super::model::Model;
use super::super::kinds::kind_page;
use super::*;

const GIZMOS: &str = "gizmos.gizmos.example.test";
const LEASES: &str = "leases.coordination.k8s.io";

fn at<'a>(resource: &'a str, name: &'a str) -> ObjectAt<'a> {
    ObjectAt {
        namespace: Some("acme-shop"),
        resource,
        name,
    }
}

/// The text of the page `asked` names, drawn with `reads`: its note,
/// then each fact, a line each.
fn page(model: &Model, reads: &Reads, asked: ObjectAt) -> Option<String> {
    let layers = &model.snapshot.resources.layers;
    let page = object_page(
        model,
        layers,
        reads,
        asked,
        k8s_openapi::jiff::Timestamp::now(),
    )?;
    let board = &page.board;
    let center = board.center.as_ref().unwrap();
    let mut text = center
        .note
        .clone()
        .map(|note| note.text)
        .unwrap_or_default();
    for section in board.row().into_iter().chain(&board.wide) {
        for fact in &section.facts {
            let values: Vec<&str> = fact.values.iter().map(|v| v.text.as_str()).collect();
            text += &format!("\n{}: {}", fact.label, values.join(", "));
        }
    }
    Some(text)
}

#[rstest]
fn a_trimmed_object_shows_its_printer_columns_from_its_store() {
    let text = page(
        &fixture::model(),
        &Reads::default(),
        at(GIZMOS, "gizmo-red"),
    )
    .unwrap();
    assert!(text.contains("Size: large"), "{text}");
    assert!(text.contains("Ready: True"), "{text}");
}

#[rstest]
fn a_kind_page_shows_every_column_and_leaves_out_wide_ones() {
    let kind = kind_page(
        &fixture::model(),
        GIZMOS,
        k8s_openapi::jiff::Timestamp::now(),
    )
    .unwrap();
    let rows: Vec<&[String]> = kind.rows.iter().map(|row| row.values.as_slice()).collect();
    assert_eq!(kind.columns, ["Ready", "Size"]);
    assert_eq!(
        rows,
        [
            ["False", "small"].map(String::from),
            ["True", "large"].map(String::from)
        ]
    );
}

fn lease() -> Arc<kube::core::DynamicObject> {
    Arc::new(
        serde_yaml::from_str(
            "apiVersion: coordination.k8s.io/v1\nkind: Lease\n\
             metadata: {name: web, namespace: acme-shop}\nspec: {holderIdentity: web-1}",
        )
        .unwrap(),
    )
}

fn lease_key() -> Key {
    read_key(&fixture::model(), at(LEASES, "web")).unwrap()
}

#[rstest]
#[case::found(Some(Read::Found(lease())), Some(READ_ONCE))]
#[case::refused(Some(Read::Refused("forbidden".into())), Some("The API server refused transect's read of this Lease. Its message: forbidden"))]
#[case::failed(Some(Read::Failed("timed out".into())), Some("Reading this Lease failed; reload to try again. The error: timed out"))]
#[case::not_kept(None, Some("This Lease has not been read; reload to read it."))]
#[case::missing(Some(Read::Missing), None)]
fn a_kind_no_store_holds_draws_its_one_read(
    #[case] read: Option<Read>,
    #[case] note: Option<&str>,
) {
    let reads = match read {
        Some(read) => Reads::holding(lease_key(), read),
        None => Reads::default(),
    };
    let text = page(&fixture::model(), &reads, at(LEASES, "web"));
    let first = text
        .as_deref()
        .map(|text| text.lines().next().unwrap_or_default());
    assert_eq!(first, note);
}

#[rstest]
#[case::a_lease(LEASES, "web", true)]
#[case::a_name_with_a_comma(LEASES, "web,other", false)]
#[case::a_name_with_an_equals_sign(LEASES, "metadata.name=web", false)]
#[case::a_kind_a_store_holds("services", "storefront-svc", false)]
#[case::a_trimmed_custom_kind(GIZMOS, "gizmo-red", false)]
#[case::a_secret("secrets", "web", false)]
fn reads_only_a_valid_name_of_a_kind_no_store_holds(
    #[case] resource: &str,
    #[case] name: &str,
    #[case] read: bool,
) {
    assert_eq!(
        read_key(&fixture::model(), at(resource, name)).is_some(),
        read
    );
}

#[rstest]
fn a_name_no_read_is_made_for_draws_no_page() {
    let reads = Reads::default();
    assert_eq!(
        page(&fixture::model(), &reads, at(LEASES, "web,other")),
        None
    );
}
