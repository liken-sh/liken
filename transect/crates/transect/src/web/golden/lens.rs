//! The masthead's `access` link is marked current on every access page,
//! and on no other page.

use rstest::rstest;

use super::render;

/// Whether the page at `url` marks the masthead's `access` link current.
fn access_current(url: &str) -> bool {
    render(url).contains(r#"class="masthead-link masthead-current" href="/access""#)
}

#[rstest]
#[case::ways_in("/access", true)]
#[case::accounts("/access/accounts", true)]
#[case::a_workloads_tab("/namespaces/acme-shop/deployments/storefront/access", true)]
#[case::a_namespaces_tab("/namespaces/acme-shop/access", true)]
#[case::a_service_account("/namespaces/acme-ops/serviceaccounts/ops-console", true)]
#[case::a_group("/access/accounts/groups/system:serviceaccounts:acme-ledger", true)]
#[case::a_user("/access/accounts/users/shop-owner", true)]
#[case::a_workloads_overview("/namespaces/acme-shop/deployments/storefront", false)]
#[case::a_namespaces_overview("/namespaces/acme-shop", false)]
fn marks_access_current_on_every_page_of_the_lens(#[case] url: &str, #[case] current: bool) {
    assert_eq!(access_current(url), current);
}
