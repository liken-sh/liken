//! The version of the scripts that a page runs. A tab can stay open
//! while `transect` moves to a new release, and the tab keeps running
//! the scripts that it loaded. When the new release changes htmx or one
//! of `transect`'s own scripts, the old scripts cannot read the
//! fragments that the new server sends: htmx 2 does not read the
//! `:inherited` attributes of htmx 4, so a click swaps a page into the
//! link that was clicked. So each page sends the version of its scripts
//! with every htmx request, in the header `Transect-Scripts`, which the
//! `hx-headers` attribute of the page's `body` sets. A request that htmx
//! sends with another version, or with none, as htmx 2 does, gets an
//! empty response with `HX-Refresh: true`, and htmx loads the whole page
//! again with the current scripts. htmx 2 and htmx 4 both obey
//! `HX-Refresh`.

use std::hash::{DefaultHasher, Hash, Hasher};
use std::sync::LazyLock;

use axum::extract::Request;
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use super::assets;

/// The header that carries the version of a page's scripts.
pub const HEADER: &str = "Transect-Scripts";

/// A hash of every script that a page loads, as the binary embeds them.
static VERSION: LazyLock<String> = LazyLock::new(|| {
    let mut hasher = DefaultHasher::new();
    for script in assets::scripts() {
        script.hash(&mut hasher);
    }
    format!("{:016x}", hasher.finish())
});

/// The version of the scripts that this server's pages load.
pub fn version() -> &'static str {
    &VERSION
}

/// Answers an htmx request from a page whose scripts are not the
/// current ones with `HX-Refresh: true`, and passes every other request
/// on. A request that a browser makes on its own, such as a page load or
/// a static file, has no `HX-Request` header and passes.
pub async fn refresh_stale(request: Request, next: Next) -> Response {
    let headers = request.headers();
    let from_htmx = headers.get("hx-request").is_some_and(|v| v == "true");
    let current = headers.get(HEADER).is_some_and(|v| v == version());
    if from_htmx && !current {
        return [("HX-Refresh", "true")].into_response();
    }
    next.run(request).await
}

#[cfg(test)]
mod tests {
    use axum::Router;
    use axum::body::Body;
    use axum::http::{Request, StatusCode};
    use axum::routing::get;
    use http_body_util::BodyExt;
    use rstest::rstest;
    use tower::ServiceExt;

    use super::*;

    /// The headers of a request from a page that loaded the scripts of
    /// `sent`, or from no page when `htmx` is false.
    async fn answer(htmx: bool, sent: Option<&str>) -> (StatusCode, Option<String>, String) {
        let router = Router::new()
            .route("/", get(|| async { "the page" }))
            .layer(axum::middleware::from_fn(refresh_stale));
        let mut request = Request::get("/");
        if htmx {
            request = request.header("HX-Request", "true");
        }
        if let Some(sent) = sent {
            request = request.header(HEADER, sent);
        }
        let response = router
            .oneshot(request.body(Body::empty()).unwrap())
            .await
            .unwrap();
        let status = response.status();
        let refresh = response
            .headers()
            .get("HX-Refresh")
            .map(|v| v.to_str().unwrap().to_owned());
        let body = response.into_body().collect().await.unwrap().to_bytes();
        (status, refresh, String::from_utf8_lossy(&body).into_owned())
    }

    #[rstest]
    #[case::a_page_load(false, None)]
    #[case::a_page_with_the_current_scripts(true, Some(version()))]
    #[tokio::test]
    async fn serves_a_request_with_the_current_scripts(
        #[case] htmx: bool,
        #[case] sent: Option<&str>,
    ) {
        assert_eq!(
            answer(htmx, sent).await,
            (StatusCode::OK, None, "the page".to_owned())
        );
    }

    #[rstest]
    #[case::a_page_with_older_scripts(Some("0000000000000000"))]
    #[case::a_page_that_sends_no_version(None)]
    #[tokio::test]
    async fn tells_a_page_with_other_scripts_to_load_again(#[case] sent: Option<&str>) {
        assert_eq!(
            answer(true, sent).await,
            (StatusCode::OK, Some("true".to_owned()), String::new())
        );
    }
}
