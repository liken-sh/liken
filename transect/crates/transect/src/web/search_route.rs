//! `/search`, the quick search's rows for the dialog and its page of
//! its own.

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

/// `/search?q=`: the rows alone for the dialog, which htmx requests
/// with its target in `HX-Target`, and a page of its own for anything
/// else.  The response sets `Vary: HX-Target`, so a cache never serves
/// the dialog's rows for a page loaded by its URL.
pub(super) async fn search(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(asked): Query<Search>,
) -> Response {
    let Some(model) = state.sources.models.last().await else {
        return StatusCode::INTERNAL_SERVER_ERROR.into_response();
    };
    let query = asked.q.as_deref().unwrap_or_default();
    let target = headers.get("hx-target").and_then(|t| t.to_str().ok());
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
            tracing::error!(%error, "rendering a search");
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }
    }
}
