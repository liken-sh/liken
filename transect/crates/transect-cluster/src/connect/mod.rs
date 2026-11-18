//! Builds the API server client. It uses HTTP/2 when the server offers
//! it, so the watches share a few connections, and sends keep-alive
//! pings to find a connection that died without a reset.
//!
//! kube's default client uses only HTTP/1.1, where each watch needs its
//! own connection, and `transect` watches every kind, including every
//! CRD. This client is built as in kube's custom-client example, from
//! the same `Config`. When the kubeconfig sets a proxy, `transect` uses
//! kube's default client instead, because only that client connects
//! through a proxy.

mod limit;
mod spread;
mod tally;

use std::time::Duration;

use hyper_rustls::{FixedServerNameResolver, HttpsConnector, HttpsConnectorBuilder};
use hyper_timeout::TimeoutConnector;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::{TokioExecutor, TokioTimer};
use kube::client::retry::RetryPolicy;
use kube::client::{Body, ConfigExt};
use kube::{Client, Config};
use tower::ServiceBuilder;
use tower::retry::RetryLayer;

use limit::Spoken;
use spread::Spread;
use tally::Tally;

/// How long a connection may receive no frame before `transect` pings it.
pub const KEEP_ALIVE: Duration = Duration::from_secs(20);

/// How long a ping may wait for a reply before `transect` closes the
/// connection. Each watch on it then lists again on a new connection.
pub const KEEP_ALIVE_TIMEOUT: Duration = Duration::from_secs(10);

/// The stream limit that `transect` assumes when it cannot read the
/// server's limit. RFC 9113, section 6.5.2, recommends that a server
/// allow at least 100 concurrent streams.
const ASSUMED: u32 = 100;

type Connector = TimeoutConnector<HttpsConnector<HttpConnector>>;

/// A client for the API server in `config`. It opens at most `most`
/// streams on one connection, and never more than `most()` allows for
/// the server's stream limit.
pub async fn client(config: Config, most: Option<usize>) -> anyhow::Result<Client> {
    if config.proxy_url.is_some() {
        tracing::info!("The kubeconfig sets a proxy, so each watch uses its own connection");
        return Ok(Client::try_from(config)?);
    }
    let connector = connector(&config)?;
    let tally = Tally::default();
    // hyper makes a request wait for a connection that is still opening
    // only when the client is HTTP/2 only. With ALPN, hyper learns the
    // protocol after the connection opens, so each watch in a burst at
    // startup or after a reset would open its own TCP connection. So
    // when `limit::spoken` negotiated h2, the clients are HTTP/2 only.
    let (limit, alpn) = match limit::spoken(&config).await {
        Ok(Spoken::Http1) => {
            tracing::info!("The API server speaks HTTP/1.1, so each watch uses its own connection");
            tally.clone().log(|| None);
            return layered(
                &config,
                tally.around(hyper_client(connector, Alpn::Offered)),
            );
        }
        Ok(Spoken::Http2(Some(limit))) => {
            tracing::info!(
                streams = limit,
                "The API server allows this many streams on one connection"
            );
            (limit, Alpn::Http2)
        }
        Ok(Spoken::Http2(None)) => {
            tracing::info!(
                assumed = ASSUMED,
                "The API server reports no stream limit, so the assumed limit applies"
            );
            (ASSUMED, Alpn::Http2)
        }
        Err(error) => {
            tracing::warn!(%error, assumed = ASSUMED, "Reading the API server's stream limit returned an error, so the assumed limit applies");
            (ASSUMED, Alpn::Offered)
        }
    };
    let most = match most {
        Some(asked) => asked.min(self::most(limit)),
        None => self::most(limit),
    };
    tracing::info!(most, "Opening at most this many streams on one connection");
    let spread = Spread::new(most, move || hyper_client(connector.clone(), alpn));
    let streams = spread.clone();
    tally.clone().log(move || Some(streams.open()));
    layered(&config, tally.around(spread))
}

/// A client that sends each request through `connections`, with the
/// layers of kube's default client in the same order: the server's base
/// URI, a retry of a 429, 503, or 504 after the time in `Retry-After`,
/// the credentials, and the extra headers. Without the retry, a single
/// request such as discovery fails on the first 429 that API Priority
/// and Fairness returns for a burst of requests.
fn layered<S, B>(config: &Config, connections: S) -> anyhow::Result<Client>
where
    S: tower::Service<http::Request<Body>, Response = http::Response<B>> + Clone + Send + 'static,
    S::Future: Send + 'static,
    S::Error: Into<tower::BoxError>,
    B: hyper::body::Body<Data = hyper::body::Bytes> + Send + 'static,
    B::Error: Into<tower::BoxError>,
{
    let retry = config
        .default_retry
        .then(|| RetryLayer::new(RetryPolicy::server_retry()));
    let service = ServiceBuilder::new()
        .layer(config.base_uri_layer())
        .option_layer(retry)
        .option_layer(config.auth_layer()?)
        .layer(config.extra_headers_layer()?)
        .map_err(Into::<tower::BoxError>::into)
        .service(connections);
    Ok(Client::new(service, config.default_namespace.clone()))
}

/// The most streams `transect` opens on one connection: the server's
/// limit minus a fifth, because the server may still count a stream
/// that the client has closed.
fn most(limit: u32) -> usize {
    (limit - limit / 5).max(1) as usize
}

/// The connector that kube's default client builds, with HTTP/2 enabled
/// as well as HTTP/1.1.
fn connector(config: &Config) -> anyhow::Result<Connector> {
    let mut http = HttpConnector::new();
    http.enforce_http(false);
    let tls = config.rustls_client_config()?;
    let mut https = HttpsConnectorBuilder::new()
        .with_tls_config(tls)
        .https_or_http();
    if let Some(name) = &config.tls_server_name {
        let name = name.clone().try_into()?;
        https = https.with_server_name_resolver(FixedServerNameResolver::new(name));
    }
    let https = https.enable_http1().enable_http2().wrap_connector(http);
    let mut connector = TimeoutConnector::new(https);
    connector.set_connect_timeout(config.connect_timeout);
    connector.set_read_timeout(config.read_timeout);
    connector.set_write_timeout(config.write_timeout);
    Ok(connector)
}

/// Which protocol a client's connections use.
#[derive(Clone, Copy)]
enum Alpn {
    /// HTTP/2 only, for a server whose TLS handshake negotiated h2.
    Http2,
    /// The protocol that the TLS handshake negotiates, HTTP/2 or
    /// HTTP/1.1.
    Offered,
}

/// A hyper client that pings its HTTP/2 connections even while they
/// are idle. With `Alpn::Offered` and a server that offers only
/// HTTP/1.1, it keeps a pool of HTTP/1.1 connections.
fn hyper_client(
    connector: Connector,
    alpn: Alpn,
) -> hyper_util::client::legacy::Client<Connector, Body> {
    hyper_util::client::legacy::Client::builder(TokioExecutor::new())
        .http2_only(matches!(alpn, Alpn::Http2))
        .timer(TokioTimer::new())
        .pool_timer(TokioTimer::new())
        .http2_keep_alive_interval(KEEP_ALIVE)
        .http2_keep_alive_timeout(KEEP_ALIVE_TIMEOUT)
        .http2_keep_alive_while_idle(true)
        .build(connector)
}

#[cfg(test)]
mod tests;
