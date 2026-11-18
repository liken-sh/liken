//! When a resource that fills a section of a page fails to load, the
//! section names the resource and links to `/resources`, and does not
//! describe what the resource would have shown.

use rstest::rstest;

use super::super::fixture;
use super::{render_from, text};

#[rstest]
#[case::usage(
    "nodeLabels: [node, instance]",
    "nodelabels: [node, instance]",
    "/",
    "no usage: the MetricsSource metrics did not load; see /resources"
)]
#[case::alerts(
    "nodeLabels: [node, instance]",
    "nodelabels: [node, instance]",
    "/",
    "the MetricsSource metrics did not load; see /resources"
)]
#[case::the_alerting_path(
    "nodeLabels: [node, instance]",
    "nodelabels: [node, instance]",
    "/?lose=node-alpha",
    "transect cannot tell whether this loss stops alerts: the MetricsSource metrics did not load; see /resources"
)]
#[case::a_volume(
    "nodeLabels: [node, instance]",
    "nodelabels: [node, instance]",
    "/volumes/ledger-archive",
    "Used bytes: the MetricsSource metrics did not load; see /resources"
)]
#[case::traffic(
    "    window: 5m",
    "    windw: 5m",
    "/namespaces/acme-shop/ingresses.networking.k8s.io/storefront-route",
    "No traffic: the IngressController edge-proxy did not load; see /resources"
)]
#[case::machine_logs(
    "    level: severity_text",
    "    levl: severity_text",
    "/machines/node-alpha",
    "No machine's own logs: the LogSource logs did not load; see /resources"
)]
#[case::code(
    "  applies:",
    "  aplies:",
    "/namespaces/acme-shop/deployments/storefront",
    "and the KindKnowledge kustomizations.kustomize.toolkit.fluxcd.io did not load; see /resources"
)]
fn names_the_resource_that_did_not_load(
    #[case] from: &str,
    #[case] to: &str,
    #[case] url: &str,
    #[case] expected: &str,
) {
    let (model, usage) = fixture::edited(|text| {
        assert!(
            text.contains(from),
            "the fixture text does not contain {from:?}"
        );
        text.replace(from, to)
    });
    let shown = text::visible(&render_from(&model, &usage, url));
    assert!(
        shown.contains(expected),
        "{url} does not contain {expected:?}:\n{shown}"
    );
    assert!(
        !shown.contains(" would "),
        "{url} contains the text \" would \", which describes a display that did not happen:\n{shown}"
    );
}
