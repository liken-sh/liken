//! A resource that loads but refers to something the cluster does not
//! have, such as a misspelled label, path, or host, must show an
//! unmatched condition on `/resources` instead of showing as loaded.

use rstest::rstest;

use super::super::fixture;
use super::{render_from, text};

#[rstest]
#[case::an_appliers_label(
    "name: {label: helm.toolkit.fluxcd.io/name}",
    "name: {label: helm.toolkit.fluxcd.io/nmae}",
    "matches nothing: applies.name: no object has the label helm.toolkit.fluxcd.io/nmae, so nothing names a HelmRelease that applied it"
)]
#[case::a_node(
    "node: metadata.name",
    "node: metdata.name",
    "matches nothing: node reads metdata.name, which names no Node on any Machine"
)]
#[case::a_reference(
    "{path: spec.storefront.name, kind: Service, label: discounts}",
    "{path: spec.storefrnt.name, kind: Service, label: discounts}",
    "matches nothing: references[0] reads spec.storefrnt.name, which no Promotion has"
)]
#[case::a_directory(
    "directory: spec.path",
    "directory: spec.pth",
    "matches nothing: directory reads spec.pth, which no Kustomization has"
)]
#[case::an_ingress_controller(
    "controller: example.test/proxy",
    "controller: example.test/proxie",
    "matches nothing: no IngressClass names the controller example.test/proxie"
)]
#[case::a_node_label(
    "nodeLabels: [node, instance]",
    "nodeLabels: [nod]",
    "matches nothing: the machine queries' series name no Node by nodeLabels nod"
)]
#[case::a_driver(
    "name: radio.example.test",
    "name: radio.example.tset",
    "matches nothing: no ResourceSlice publishes a device of the driver radio.example.tset"
)]
#[case::a_forges_host(
    "hosts: [github.com]",
    "hosts: [github.example]",
    "matches nothing: no repository transect reads, from a GitOps source or an image's source label, is on a host it names or serves"
)]
fn shows_a_reference_that_matches_nothing(
    #[case] from: &str,
    #[case] to: &str,
    #[case] expected: &str,
) {
    let (model, usage) = fixture::edited(|text| {
        assert!(
            text.contains(from),
            "the fixture text does not contain {from:?}"
        );
        text.replace(from, to)
    });
    let shown = text::visible(&render_from(&model, &usage, "/resources"));
    assert!(
        shown.contains(expected),
        "the /resources page does not contain {expected:?}:\n{shown}"
    );
}
