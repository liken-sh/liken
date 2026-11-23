//! The static files: styles, fonts, and htmx. Release builds embed
//! every file in the binary, so a page loads nothing from another
//! server. Debug builds read each file from the source tree on every
//! request, so a style edit shows on reload without a rebuild.
//!
//! Each response has an `ETag`, a hash of the file's bytes, and
//! `Cache-Control: no-cache`. So the browser revalidates its cached copy
//! on every page load, and `transect` responds 304 with no body while
//! the `ETag` matches. A new build, or a style edit in a debug build,
//! shows on the next load.

use std::hash::{DefaultHasher, Hash, Hasher};

use axum::extract::Path;
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};

struct Asset {
    /// The path after `/static/`.
    url: &'static str,
    /// The path relative to `crates/`, usually under a crate's `src/`,
    /// because each module keeps its styles beside its templates. The
    /// fonts are read from `brand/fonts/` at the top of the repository,
    /// so the binary embeds the brand's own files and the workspace
    /// keeps no copy.
    source: &'static str,
    content_type: &'static str,
    /// What release builds serve. Debug builds read `source` instead,
    /// and `scripts` reads this copy in both.
    embedded: &'static [u8],
}

macro_rules! asset {
    ($url:literal, $source:literal, $content_type:expr) => {
        Asset {
            url: $url,
            source: $source,
            content_type: $content_type,
            embedded: include_bytes!(concat!(env!("CARGO_MANIFEST_DIR"), "/../", $source)),
        }
    };
}

const CSS: &str = "text/css; charset=utf-8";
const JS: &str = "text/javascript; charset=utf-8";
const SVG: &str = "image/svg+xml";
const WOFF2: &str = "font/woff2";

const ASSETS: &[Asset] = &[
    asset!(
        "brand/liken.css",
        "transect-frame/src/frame/brand/liken.css",
        CSS
    ),
    asset!(
        "brand/fonts/SourceSans3VF-Upright.otf.woff2",
        "../../brand/fonts/SourceSans3VF-Upright.otf.woff2",
        WOFF2
    ),
    asset!(
        "brand/fonts/SourceSans3VF-Italic.otf.woff2",
        "../../brand/fonts/SourceSans3VF-Italic.otf.woff2",
        WOFF2
    ),
    asset!(
        "brand/transect.svg",
        "transect-frame/src/frame/brand/transect.svg",
        SVG
    ),
    asset!("web/layout.css", "transect/src/web/layout.css", CSS),
    asset!("map/cluster.css", "transect-map/src/map/cluster.css", CSS),
    asset!("map/gone.css", "transect-map/src/map/gone.css", CSS),
    asset!("map/sidebar.css", "transect-map/src/map/sidebar.css", CSS),
    asset!(
        "map/namespace/namespace.css",
        "transect-map/src/map/namespace/namespace.css",
        CSS
    ),
    asset!(
        "map/workload/workload.css",
        "transect-map/src/map/workload/workload.css",
        CSS
    ),
    asset!("map/pod/pod.css", "transect-map/src/map/pod/pod.css", CSS),
    asset!(
        "map/section/section.css",
        "transect-map/src/map/section/section.css",
        CSS
    ),
    asset!(
        "lenses/lenses.css",
        "transect-map/src/lenses/lenses.css",
        CSS
    ),
    asset!(
        "map/gitops/gitops.css",
        "transect-map/src/map/gitops/gitops.css",
        CSS
    ),
    asset!(
        "map/known/known.css",
        "transect-map/src/map/known/known.css",
        CSS
    ),
    asset!(
        "map/known/sketch.css",
        "transect-map/src/map/known/sketch.css",
        CSS
    ),
    asset!(
        "shapes/shapes.css",
        "transect-map/src/shapes/shapes.css",
        CSS
    ),
    asset!("shapes/text.css", "transect-map/src/shapes/text.css", CSS),
    asset!(
        "shapes/specimen.css",
        "transect-map/src/shapes/specimen.css",
        CSS
    ),
    asset!(
        "objects/objects.css",
        "transect-map/src/objects/objects.css",
        CSS
    ),
    asset!(
        "objects/board.css",
        "transect-map/src/objects/board.css",
        CSS
    ),
    asset!(
        "map/machine/machine.css",
        "transect-map/src/map/machine/machine.css",
        CSS
    ),
    asset!(
        "cards/namespace.css",
        "transect-map/src/cards/namespace.css",
        CSS
    ),
    asset!(
        "cards/workload.css",
        "transect-map/src/cards/workload.css",
        CSS
    ),
    asset!(
        "machines/machines.css",
        "transect-ground/src/machines/machines.css",
        CSS
    ),
    asset!(
        "alerts/alerts.css",
        "transect-observe/src/alerts/alerts.css",
        CSS
    ),
    asset!("loss/loss.css", "transect-ground/src/loss/loss.css", CSS),
    asset!(
        "images/origin.css",
        "transect-supply/src/images/origin.css",
        CSS
    ),
    asset!(
        "images/runs.css",
        "transect-supply/src/images/runs.css",
        CSS
    ),
    asset!(
        "intent/intent.css",
        "transect-resources/src/intent/intent.css",
        CSS
    ),
    asset!(
        "storage/ground.css",
        "transect-ground/src/storage/ground.css",
        CSS
    ),
    asset!(
        "resources/resources.css",
        "transect-resources/src/resources/resources.css",
        CSS
    ),
    asset!(
        "changed/changed.css",
        "transect-supply/src/changed/changed.css",
        CSS
    ),
    asset!(
        "vulnerabilities/vulnerabilities.css",
        "transect-supply/src/vulnerabilities/vulnerabilities.css",
        CSS
    ),
    asset!(
        "access/access.css",
        "transect-access/src/access/access.css",
        CSS
    ),
    // htmx.org 4.0.2, dist/htmx.min.js
    asset!("htmx.min.js", "transect/src/web/static/htmx.min.js", JS),
    asset!("web/links.js", "transect/src/web/links.js", JS),
    asset!("search/search.js", "transect-map/src/search/search.js", JS),
    asset!(
        "map/known/wires.js",
        "transect-map/src/map/known/wires.js",
        JS
    ),
    asset!(
        "search/search.css",
        "transect-map/src/search/search.css",
        CSS
    ),
    // htmx.org 4.0.2, dist/ext/hx-sse.min.js
    asset!("hx-sse.min.js", "transect/src/web/static/hx-sse.min.js", JS),
];

pub async fn serve(Path(url): Path<String>, headers: HeaderMap) -> Response {
    let Some(asset) = ASSETS.iter().find(|asset| asset.url == url) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    match contents(asset).await {
        Ok(bytes) => {
            let tag = etag(&bytes);
            let validators = [
                (header::ETAG, tag.clone()),
                (header::CACHE_CONTROL, "no-cache".into()),
            ];
            let asked = headers.get(header::IF_NONE_MATCH);
            if asked.is_some_and(|asked| asked.as_bytes() == tag.as_bytes()) {
                return (StatusCode::NOT_MODIFIED, validators).into_response();
            }
            let typed = [(header::CONTENT_TYPE, asset.content_type)];
            (typed, validators, bytes).into_response()
        }
        Err(error) => {
            tracing::error!(source = asset.source, %error, "A static file is unreadable");
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }
    }
}

/// The bytes of every script that a page loads, which `scripts`
/// hashes into the version of a page's scripts.
pub fn scripts() -> impl Iterator<Item = &'static [u8]> {
    ASSETS
        .iter()
        .filter(|asset| asset.content_type == JS)
        .map(|asset| asset.embedded)
}

/// A quoted hash of `bytes`, which changes whenever the file does.
fn etag(bytes: &[u8]) -> String {
    let mut hasher = DefaultHasher::new();
    bytes.hash(&mut hasher);
    format!("\"{:016x}\"", hasher.finish())
}

#[cfg(debug_assertions)]
async fn contents(asset: &Asset) -> std::io::Result<Vec<u8>> {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join(asset.source);
    tokio::fs::read(path).await
}

#[cfg(not(debug_assertions))]
async fn contents(asset: &Asset) -> std::io::Result<&'static [u8]> {
    Ok(asset.embedded)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// Which copy of a file the browser has cached.
    enum Cached {
        Nothing,
        TheCurrentCopy,
        AnOlderCopy,
    }

    /// The headers a browser sends for `url` when its cache has `cached`.
    async fn sent(url: &str, cached: Cached) -> HeaderMap {
        let tag = match cached {
            Cached::Nothing => return HeaderMap::new(),
            Cached::TheCurrentCopy => {
                serve(Path(url.into()), HeaderMap::new()).await.headers()[header::ETAG].clone()
            }
            Cached::AnOlderCopy => "\"0\"".parse().unwrap(),
        };
        HeaderMap::from_iter([(header::IF_NONE_MATCH, tag)])
    }

    #[rstest]
    #[case::a_first_request("web/layout.css", Cached::Nothing, StatusCode::OK)]
    #[case::the_current_copy("web/layout.css", Cached::TheCurrentCopy, StatusCode::NOT_MODIFIED)]
    #[case::an_older_copy("web/layout.css", Cached::AnOlderCopy, StatusCode::OK)]
    #[case::no_such_file("web/nowhere.css", Cached::Nothing, StatusCode::NOT_FOUND)]
    #[tokio::test]
    async fn responds_not_modified_only_to_the_current_etag(
        #[case] url: &str,
        #[case] cached: Cached,
        #[case] status: StatusCode,
    ) {
        let headers = sent(url, cached).await;
        assert_eq!(serve(Path(url.into()), headers).await.status(), status);
    }

    /// Every page loads each file that the frame's head names, so a
    /// file the head names and this list lacks leaves a page unstyled.
    #[rstest]
    fn serves_every_file_that_the_frame_loads() {
        let head = include_str!("../../../transect-frame/src/frame/layout.html");
        let named = head.split("\"/static/").skip(1);
        let named = named.filter_map(|rest| rest.split('"').next());
        let missing: Vec<&str> = named
            .filter(|url| !ASSETS.iter().any(|asset| asset.url == *url))
            .collect();
        assert_eq!(missing, Vec::<&str>::new());
    }

    #[rstest]
    #[tokio::test]
    async fn tells_the_browser_to_revalidate_every_time() {
        let response = serve(Path("web/layout.css".into()), HeaderMap::new()).await;
        assert_eq!(response.headers()[header::CACHE_CONTROL], "no-cache");
    }
}
