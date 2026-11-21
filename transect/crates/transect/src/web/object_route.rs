//! Routes for the pages of one object in a namespace: a workload's or
//! a custom resource's page, its section view, one elevation of its
//! sketch, its logs, and a pod's page.

use axum::extract::{Path, Query, State};
use axum::response::Response;
use k8s_openapi::jiff::Timestamp;

use super::view::{self, Page};
use super::{AppState, pages};

pub(super) async fn workload(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let page = Page::Workload {
        namespace,
        resource,
        name,
    };
    state.whole(page, &query).await
}

pub(super) async fn section(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let page = Page::Section {
        namespace,
        resource,
        name,
    };
    state.whole(page, &query).await
}

/// One elevation of a custom resource whose kind's knowledge states
/// elevations.
pub(super) async fn elevation(
    State(state): State<AppState>,
    Path((namespace, resource, name, slug)): Path<(String, String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let page = Page::Elevation {
        namespace,
        resource,
        name,
        slug,
    };
    state.whole(page, &query).await
}

pub(super) async fn pod(
    State(state): State<AppState>,
    Path((namespace, name)): Path<(String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    // Log tails are read on a page load, never for a push.
    let last = state.sources.models.last().await;
    let pod = last
        .iter()
        .flat_map(|model| &model.snapshot.pods)
        .find(|pod| {
            pod.metadata.namespace.as_deref() == Some(namespace.as_str())
                && pod.metadata.name.as_deref() == Some(name.as_str())
        });
    // The page renders from the last model but reads its tails from the
    // cache, so it shows the new tails. When a read finishes, `refresh`
    // signals a change, and the push that follows sends the health
    // counts from the new tails.
    if let Some(pod) = pod {
        state.sources.logs.refresh(pod).await;
    }
    state
        .whole_on(last, Page::Pod { namespace, name }, &query)
        .await
}

pub(super) async fn logs(
    State(state): State<AppState>,
    Path((namespace, resource, name)): Path<(String, String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let model = state.sources.models.last().await;
    let (pod, container) = (query.pod(), query.container());
    let lens = pages::Lens {
        namespace: &namespace,
        resource: &resource,
        name: &name,
        pod: pod.as_deref(),
        container: container.as_deref(),
    };
    // As in `pod`, read the tails on a page load.
    if let Some(pod) = model
        .as_deref()
        .and_then(|model| pages::shown_pod(model, &lens, Timestamp::now()))
    {
        state.sources.logs.refresh(&pod).await;
    }
    let page = Page::Logs {
        namespace: namespace.clone(),
        resource: resource.clone(),
        name: name.clone(),
        pod,
        container,
    };
    state.whole_on(model, page, &query).await
}
