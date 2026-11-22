//! Routes for the access pages whose URLs carry path segments: a
//! namespace's access tab, a group's or a user's page, and a
//! workload's access tab.

use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};

use super::AppState;
use super::view::Page;

pub(super) async fn namespace(State(state): State<AppState>, Path(name): Path<String>) -> Response {
    state.whole(Page::NamespaceAccess { name }).await
}

pub(super) async fn subject(
    State(state): State<AppState>,
    Path((kinds, name)): Path<(String, String)>,
) -> Response {
    match kinds.as_str() {
        "groups" | "users" => state.whole(Page::Subject { kinds, name }).await,
        _ => StatusCode::NOT_FOUND.into_response(),
    }
}

pub(super) async fn workload(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
) -> Response {
    let page = Page::Access {
        namespace,
        resource,
        name,
    };
    state.whole(page).await
}
