//! The HTTP server: one route per elevation, the event stream that keeps
//! an open page current, and the static files.

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
    /// The `main` block of each view a browser has open.
    views: Views<View>,
}

/// What the pages read besides the cluster's stores.
pub struct Inputs {
    pub resources: watch::Receiver<Arc<Loaded>>,
    pub usage: watch::Receiver<usage::Reading>,
    /// The alerts that Alertmanager last reported as firing.
    pub alerts: watch::Receiver<alerts::Reading>,
    pub logs: LogTails,
    /// The log source's last round of searches across the cluster.
    pub searched: watch::Receiver<Arc<Searched>>,
    pub images: watch::Receiver<Arc<Known>>,
    /// What the ingress controllers' metrics give for each backend.
    pub entry: watch::Receiver<transect_network::entry::poll::Reading>,
    /// The bytes each claim's volume holds, from the metrics source.
    pub volumes_used: watch::Receiver<transect_observe::metrics::Reading>,
    /// The pod name Prometheus runs under.
    pub prometheus_pod: watch::Receiver<Option<String>>,
    /// The objects of kinds no store holds, read once per page load.
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

/// Serves the fixture cluster the tests build, which never changes, and
/// reads nothing from the network: no API server, no Prometheus, and
/// no log store.  It is for work on templates, CSS, and layout without
/// a cluster.
#[cfg(debug_assertions)]
pub async fn serve_fixture(listen: SocketAddr) -> anyhow::Result<()> {
    let (changes, _) = watch::channel(());
    serve_sources(listen, &changes, fixture_sources(&changes)?).await
}

/// What the fixture's pages read: its one model, its usage, log tails
/// that read nothing, and the objects it holds as read once.
#[cfg(any(test, debug_assertions))]
fn fixture_sources(changes: &watch::Sender<()>) -> anyhow::Result<Sources> {
    // The log store's HTTP client is built though it sends nothing, and
    // it needs a TLS provider, which a live start installs on its
    // first connection to the API server.
    let _ = tokio_rustls::rustls::crypto::ring::default_provider().install_default();
    Ok(Sources {
        models: Latest::new(changes, |_: Option<&Model>| fixture::model()),
        usage: watch::channel(fixture::usage()).1,
        logs: LogTails::offline(changes.clone(), transect_network::addresses::DEFAULT_DOMAIN)?,
        reads: fixture::reads(),
    })
}

/// Serves every page from `sources`, rendering each open view again
/// when `changes` marks a change.
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

/// Every route, drawn from `sources`.
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
        // A static segment outranks a parameter, so `access` reaches a
        // namespace's access tab and never a list of a kind.
        .route(
            "/namespaces/{namespace}/access",
            get(access_route::namespace),
        )
        // A static segment outranks a parameter, so `pods` reaches the
        // pod page and never a workload's.
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
        // A page connects to `/events` followed by its own path and query,
        // so the stream renders exactly the view the page shows.
        .route("/events/", get(view_events_at_root))
        .route("/events/{*path}", get(view_events))
        .route("/static/{*path}", get(assets::serve))
        .merge(probes::routes())
        .with_state(state)
}

impl AppState {
    /// The page whole, with 404 when it reports its object gone.  A
    /// page of an object no store holds reads it first.  It draws from
    /// the last model built and never waits for a build, because the
    /// cluster changes every few seconds, a build takes from a tenth of a
    /// second to most of a second, and a click that waited would stall.
    /// The page's event stream sends anything newer.  The one exception
    /// is a page whose object the last model does not hold while a newer
    /// model is due, such as a link to an object created a second ago:
    /// it waits for that build before it answers 404.
    async fn whole(&self, page: Page, query: &view::Query) -> Response {
        self.sources.prepare(&page).await;
        let model = self.sources.models.last().await;
        self.whole_on(model, page, query).await
    }

    /// The page whole, drawn from `model`, or when `model` does not hold
    /// its object and a newer model is due, from that one.
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

/// An object outside any namespace.  A `Node` redirects to its
/// machine's page, and a `Namespace` to its elevation.
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
    // The tails are read here, on a page load, and never for a push.
    let last = state.sources.models.last().await;
    let pod = last
        .iter()
        .flat_map(|model| &model.snapshot.pods)
        .find(|pod| {
            pod.metadata.namespace.as_deref() == Some(namespace.as_str())
                && pod.metadata.name.as_deref() == Some(name.as_str())
        });
    // The read marks the cluster changed.  The page draws from the last
    // model built and takes its tails from the cache, so it shows them
    // anyway, and the push that follows the change sends the health
    // counts taken from them.
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
    // The tails are read here, on a page load, as the pod page reads
    // them, and the page draws them from the cache.
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

async fn view_events_at_root(state: State<AppState>, query: Query<view::Query>) -> Response {
    view_events(state, Path(String::new()), query).await
}

async fn view_events(
    State(state): State<AppState>,
    Path(path): Path<String>,
    Query(query): Query<view::Query>,
) -> Response {
    let Some(view) = View::at(&path, query) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    // A stream that connects with no page load before it, or after
    // `Reads` dropped the page's copy, reads the object as a page load
    // would.
    state.sources.prepare(&view.page).await;
    events::stream(state.views.subscribe(view).await).into_response()
}
