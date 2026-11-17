//! The access tabs and pages whose URLs have their own segments: a
//! namespace's access, a group's or a user's, and a workload's.

use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};

use super::AppState;
use super::view::{self, Page};

pub(super) async fn namespace(
    State(state): State<AppState>,
    Path(name): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::NamespaceAccess { name }, &query).await
}

pub(super) async fn subject(
    State(state): State<AppState>,
    Path((kinds, name)): Path<(String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    match kinds.as_str() {
        "groups" | "users" => state.whole(Page::Subject { kinds, name }, &query).await,
        _ => StatusCode::NOT_FOUND.into_response(),
    }
}

pub(super) async fn workload(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let page = Page::Access {
        namespace,
        resource,
        name,
    };
    state.whole(page, &query).await
}
