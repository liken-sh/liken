//! The liveness and readiness probes. `transect` binds its port only
//! after the first list of the cluster finishes, so until then the
//! probes fail with a refused connection. After that they always
//! return `ok`. They do not check the API server, because during an
//! API server outage the `Service` must keep routing to `transect` so
//! that a person can still read the last state it saw.

use axum::Router;
use axum::routing::get;

pub fn routes<S: Clone + Send + Sync + 'static>() -> Router<S> {
    Router::new()
        .route("/livez", get(ok))
        .route("/readyz", get(ok))
}

async fn ok() -> &'static str {
    "ok"
}
