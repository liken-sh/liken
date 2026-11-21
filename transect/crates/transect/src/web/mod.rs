//! The HTTP server. It serves a page for each elevation, an event
//! stream that pushes updates to an open page, and the static files.

mod access_route;
mod assets;
mod carry;
#[cfg(test)]
mod crawl;
mod debounce;
mod events;
#[cfg(any(test, debug_assertions))]
pub(crate) mod fixture;
#[cfg(test)]
mod golden;
mod latest;
pub mod loaded;
mod model;
mod pages;
mod probes;
mod render;
#[cfg(test)]
mod scale;
mod search_route;
#[cfg(test)]
mod served;
mod shutdown;
mod snapshot;
mod template;
mod view;
mod views;
mod vulnerabilities_route;

use k8s_openapi::jiff::Timestamp;
use std::net::SocketAddr;
use std::sync::Arc;

use axum::Router;
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::{Html, IntoResponse, Redirect, Response};
use axum::routing::get;
use tokio::sync::watch;

use crate::web::loaded::Loaded;
use latest::Latest;
use model::Model;
use render::{Part, Rendered, Sources};
use snapshot::Signals;
use transect_cluster::cluster::Cluster;
use transect_map::objects::reads::Reads;
use transect_observe::alerts;
use transect_observe::logs::{Counted, LogTails, Searched};
use transect_observe::usage;
use transect_supply::images::Known;
use view::{Page, View};
use views::Views;

#[derive(Clone)]
struct AppState {
    sources: Sources,
    /// Re-renders the `main` block of each view that a browser has
    /// open.
    views: Views<View>,
}

/// The sources the pages read in addition to the cluster's stores.
pub struct Inputs {
    pub resources: watch::Receiver<Arc<Loaded>>,
    pub usage: watch::Receiver<usage::Reading>,
    /// The alerts that Alertmanager most recently reported as firing.
    pub alerts: watch::Receiver<alerts::Reading>,
    pub logs: LogTails,
    /// The results of the latest round of log searches across the
    /// cluster.
    pub searched: watch::Receiver<Arc<Searched>>,
    pub images: watch::Receiver<Arc<Known>>,
    /// Per-backend traffic figures from the ingress controllers'
    /// metrics.
    pub entry: watch::Receiver<transect_network::entry::poll::Reading>,
    /// The bytes used on each claim's volume, from metrics.
    pub volumes_used: watch::Receiver<transect_observe::metrics::Reading>,
    /// The name of the pod that runs Prometheus, once one is found.
    pub prometheus_pod: watch::Receiver<Option<String>>,
    /// Objects of kinds that no store watches, read once per page load.
    pub reads: Reads,
}

pub async fn serve(listen: SocketAddr, cluster: Cluster, inputs: Inputs) -> anyhow::Result<()> {
    let Inputs {
        resources,
        usage,
        alerts,
        logs,
        searched,
        images,
        entry,
        volumes_used,
        prometheus_pod,
        reads,
    } = inputs;
    let models = Latest::new(&cluster.change_signal(), {
        let (cluster, logs) = (cluster.clone(), logs.clone());
        move |previous: Option<&Model>| {
            let images = images.borrow().clone();
            let resources = resources.borrow().clone();
            let alerts = alerts.borrow().clone();
            let counted = Counted {
                logged: logs.logged(),
                searched: searched.borrow().clone(),
            };
            let signals = Signals {
                logs: counted,
                alerts,
                entry: entry.borrow().clone(),
                volumes_used: volumes_used.borrow().clone(),
                prometheus_pod: prometheus_pod.borrow().clone(),
            };
            Model::take(&cluster, images, resources, signals, previous)
        }
    });
    let sources = Sources {
        models,
        usage,
        logs,
        reads,
    };
    serve_sources(listen, &cluster.change_signal(), sources).await
}

/// Serves the fixture cluster that the tests build. It makes no network
/// requests, so you can work on templates, CSS, and layout without a
/// cluster.
#[cfg(debug_assertions)]
pub async fn serve_fixture(listen: SocketAddr) -> anyhow::Result<()> {
    let (changes, _) = watch::channel(());
    serve_sources(listen, &changes, fixture_sources(&changes)?).await
}

#[cfg(any(test, debug_assertions))]
fn fixture_sources(changes: &watch::Sender<()>) -> anyhow::Result<Sources> {
    // `LogTails::offline` builds a `reqwest` client, which fails without
    // a process-wide rustls crypto provider. A live start installs one
    // when it connects to the API server, and the fixture connects to
    // none.
    let _ = tokio_rustls::rustls::crypto::ring::default_provider().install_default();
    Ok(Sources {
        models: Latest::new(changes, |_: Option<&Model>| fixture::model()),
        usage: watch::channel(fixture::usage()).1,
        logs: LogTails::offline(changes.clone(), transect_network::addresses::DEFAULT_DOMAIN)?,
        reads: fixture::reads(),
    })
}

/// Serves every page from `sources` until shutdown, and renders each
/// open view again when `changes` fires.
async fn serve_sources(
    listen: SocketAddr,
    changes: &watch::Sender<()>,
    sources: Sources,
) -> anyhow::Result<()> {
    let router = router(changes, sources);
    let listener = tokio::net::TcpListener::bind(listen).await?;
    tracing::info!("serving on http://{listen}");
    shutdown::serve_until(listener, router, shutdown::signal(), shutdown::GRACE).await?;
    Ok(())
}

fn router(changes: &watch::Sender<()>, sources: Sources) -> Router {
    let views = Views::new(changes.clone(), debounce::QUIET, debounce::CAP, {
        let sources = sources.clone();
        move |view: View| {
            let sources = sources.clone();
            async move { sources.render(view, Part::Main).await.ok().map(|r| r.html) }
        }
    });
    let state = AppState { sources, views };
    Router::new()
        .route("/", get(cluster_elevation))
        .route("/resources", get(resources_page))
        .route("/changed", get(changed_page))
        .route("/kinds", get(kinds_list))
        .route("/shapes", get(shapes_list))
        .route("/access", get(ways_page))
        .route("/access/accounts", get(accounts_page))
        .route(
            "/access/accounts/{kinds}/{name}",
            get(access_route::subject),
        )
        .route("/vulnerabilities", get(vulnerabilities_route::list))
        .route(
            "/vulnerabilities/{*id}",
            get(vulnerabilities_route::finding),
        )
        .route("/kinds/{resource}", get(kind_list))
        .route("/search", get(search_route::search))
        .route("/machines/{name}", get(machine_elevation))
        .route("/ground/{name}", get(ground_elevation))
        .route("/volumes/{name}", get(volume_elevation))
        .route("/cluster/{resource}/{name}", get(cluster_object))
        .route("/namespaces/{namespace}", get(namespace_elevation))
        .route("/namespaces/{namespace}/{resource}", get(objects_list))
        // axum matches a static segment before a `{parameter}`, so
        // `/namespaces/{namespace}/access` is a namespace's access tab
        // and not a list of a resource named `access`.
        .route(
            "/namespaces/{namespace}/access",
            get(access_route::namespace),
        )
        // For the same reason, `/namespaces/{namespace}/pods/{name}` is
        // the pod page and not a workload's.
        .route("/namespaces/{namespace}/pods/{name}", get(pod_elevation))
        .route(
            "/namespaces/{namespace}/{resource}/{name}",
            get(workload_elevation),
        )
        .route(
            "/namespaces/{namespace}/{resource}/{name}/section",
            get(section_elevation),
        )
        .route(
            "/namespaces/{namespace}/{resource}/{name}/access",
            get(access_route::workload),
        )
        .route(
            "/namespaces/{namespace}/{resource}/{name}/vulnerabilities",
            get(vulnerabilities_route::workload),
        )
        .route(
            "/namespaces/{namespace}/{resource}/{name}/logs",
            get(logs_lens),
        )
        // A page opens `/events` followed by its own path and query, so
        // the stream renders the same view that the page shows.
        .route("/events/", get(events::page_at_root))
        .route("/events/{*path}", get(events::page))
        .route("/static/{*path}", get(assets::serve))
        .merge(probes::routes())
        .with_state(state)
}

impl AppState {
    /// Renders `page` whole, with status 404 when its object is gone.
    /// It renders from the last model built and does not wait for a new
    /// one, because the cluster changes often and waiting for a slow
    /// build would delay each click. The page's event stream sends
    /// anything newer. When the object is missing from the last model
    /// and a newer model is due, as for a link to an object created a
    /// second ago, it waits for that model before it responds 404.
    async fn whole(&self, page: Page, query: &view::Query) -> Response {
        self.sources.prepare(&page).await;
        let model = self.sources.models.last().await;
        self.whole_on(model, page, query).await
    }

    /// Renders `page` whole from `model`, or from a newer model as
    /// `whole` describes.
    async fn whole_on(
        &self,
        model: Option<Arc<Model>>,
        page: Page,
        query: &view::Query,
    ) -> Response {
        let view = View {
            page,
            shown: query.shown(),
        };
        let models = &self.sources.models;
        let mut rendered = self
            .sources
            .render_on(model, view.clone(), Part::Whole)
            .await;
        if matches!(rendered, Ok(Rendered { found: false, .. })) && models.behind() {
            rendered = self
                .sources
                .render_on(models.get().await, view, Part::Whole)
                .await;
        }
        match rendered {
            Ok(Rendered { html, found: true }) => Html(html).into_response(),
            Ok(Rendered { html, found: false }) => {
                (StatusCode::NOT_FOUND, Html(html)).into_response()
            }
            Err(status) => status.into_response(),
        }
    }
}

async fn cluster_elevation(
    State(state): State<AppState>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::cluster(&query), &query).await
}

async fn resources_page(
    State(state): State<AppState>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Resources, &query).await
}

async fn changed_page(State(state): State<AppState>, Query(query): Query<view::Query>) -> Response {
    state.whole(Page::Changed, &query).await
}

async fn kinds_list(State(state): State<AppState>, Query(query): Query<view::Query>) -> Response {
    state.whole(Page::Kinds, &query).await
}

async fn shapes_list(State(state): State<AppState>, Query(query): Query<view::Query>) -> Response {
    state.whole(Page::Shapes, &query).await
}

async fn ways_page(State(state): State<AppState>, Query(query): Query<view::Query>) -> Response {
    state.whole(Page::WaysIn, &query).await
}

async fn accounts_page(
    State(state): State<AppState>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Accounts, &query).await
}

async fn kind_list(
    State(state): State<AppState>,
    Path(resource): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Kind { resource }, &query).await
}

async fn machine_elevation(
    State(state): State<AppState>,
    Path(name): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Machine { name }, &query).await
}

async fn ground_elevation(
    State(state): State<AppState>,
    Path(name): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Ground { name }, &query).await
}

async fn volume_elevation(
    State(state): State<AppState>,
    Path(name): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Volume { name }, &query).await
}

async fn namespace_elevation(
    State(state): State<AppState>,
    Path(name): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    state.whole(Page::Namespace { name }, &query).await
}

async fn objects_list(
    State(state): State<AppState>,
    Path((namespace, resource)): Path<(String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    let page = Page::Objects {
        namespace,
        resource,
    };
    state.whole(page, &query).await
}

/// A page for a cluster-scoped object. A `Node`, a `PersistentVolume`,
/// and a `Namespace` redirect permanently to their machine, volume, and
/// namespace pages.
async fn cluster_object(
    State(state): State<AppState>,
    Path((resource, name)): Path<(String, String)>,
    Query(query): Query<view::Query>,
) -> Response {
    match resource.as_str() {
        "nodes" => {
            return Redirect::permanent(&transect_graph::links::machine(&name)).into_response();
        }
        "persistentvolumes" => {
            return Redirect::permanent(&transect_graph::links::volume(&name)).into_response();
        }
        "namespaces" => {
            return Redirect::permanent(&transect_graph::links::namespace(&name)).into_response();
        }
        _ => {}
    }
    state
        .whole(Page::ClusterObject { resource, name }, &query)
        .await
}

async fn workload_elevation(
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

async fn section_elevation(
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

async fn pod_elevation(
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

async fn logs_lens(
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
    // As in `pod_elevation`, read the tails on a page load.
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
