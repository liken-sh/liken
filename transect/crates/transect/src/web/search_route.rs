//! The `/search` route. It serves the result rows of the search dialog,
//! and a full search page when the URL is opened directly.

use askama::Template;
use axum::extract::{Query, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{Html, IntoResponse, Response};

use super::{AppState, pages};
use transect_map::search;

#[derive(serde::Deserialize)]
pub(super) struct Search {
    q: Option<String>,
}

/// Handles `/search?q=`. When `HX-Target` is the dialog's or the
/// page's result list, the response is only the result rows, and any
/// other request gets the full search page. htmx writes `HX-Target` as
/// the target's tag and id, such as `ol#search-results`, so the route
/// compares the part after the `#`. The response sets `Vary: HX-Target`
/// so that a cache does not serve the rows alone to a browser that
/// opens the URL directly.
pub(super) async fn search(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(params): Query<Search>,
) -> Response {
    let Some(model) = state.sources.models.last().await else {
        return StatusCode::INTERNAL_SERVER_ERROR.into_response();
    };
    let query = params.q.as_deref().unwrap_or_default();
    let target = headers
        .get("hx-target")
        .and_then(|t| t.to_str().ok())
        .and_then(|t| t.split_once('#'))
        .map(|(_, id)| id);
    let fragment = [search::DIALOG_LIST, search::PAGE_LIST]
        .into_iter()
        .find(|list| target == Some(*list));
    let html = match fragment {
        Some(list) => pages::search_results(&model, query, list).render(),
        None => {
            let results = pages::search_results(&model, query, search::PAGE_LIST);
            let described: &dyn std::any::Any = &model.vulnerabilities.described;
            let values: [(&str, &dyn std::any::Any); 1] = [("vulnerabilities", described)];
            transect_map::search::SearchPage { results }.render_with_values(&values)
        }
    };
    match html {
        Ok(html) => ([(header::VARY, "HX-Target")], Html(html)).into_response(),
        Err(error) => {
            tracing::error!(%error, "The search page did not render");
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }
    }
}
