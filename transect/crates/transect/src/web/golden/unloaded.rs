//! What a page says when a resource that would fill one of its
//! sections is there but did not load: it names the resource and sends
//! the reader to `/resources`, and never says that one would show it.

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
    #[case] says: &str,
) {
    let (model, usage) = fixture::edited(|text| {
        assert!(text.contains(from), "the fixture holds no {from:?}");
        text.replace(from, to)
    });
    let shown = text::visible(&render_from(&model, &usage, url));
    assert!(
        shown.contains(says),
        "{url} does not say {says:?}:\n{shown}"
    );
    assert!(
        !shown.contains(" would "),
        "{url} still says what would show it:\n{shown}"
    );
}
