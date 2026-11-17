//! The kubelet's probes.  `transect` binds its port only after the
//! first list of the cluster finishes, so until then every probe is
//! refused, which the kubelet counts as a failure.  Once the port
//! answers, both probes pass for as long as the process runs, because
//! an API server outage that made `transect` unready would remove it
//! from its `Service` when an operator most wants to look, so a stale
//! store shows on the page instead.

use axum::Router;
use axum::routing::get;

/// `/livez` and `/readyz`, which answer `ok` with no state of their own.
pub fn routes<S: Clone + Send + Sync + 'static>() -> Router<S> {
    Router::new()
        .route("/livez", get(ok))
        .route("/readyz", get(ok))
}

async fn ok() -> &'static str {
    "ok"
}
