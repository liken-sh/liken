//! Routes for the vulnerabilities pages: the list of vulnerability ids
//! in current scanner reports, filtered by the severities in the query,
//! the page of one id, and a workload's tab.

use axum::extract::{Path, Query, State};
use axum::response::Response;

use super::AppState;
use super::view::{self, Page};

pub(super) async fn list(
    State(state): State<AppState>,
    Query(query): Query<view::Query>,
) -> Response {
    let severities = query.severities();
    state
        .whole(Page::Vulnerabilities { severities }, &query)
        .await
}

pub(super) async fn workload(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let severities = query.severities();
    let page = Page::WorkloadVulnerabilities {
        namespace,
        resource,
        name,
        severities,
    };
    state.whole(page, &query).await
}

pub(super) async fn finding(
    State(state): State<AppState>,
    Path(id): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Vulnerability { id }, &query).await
}
