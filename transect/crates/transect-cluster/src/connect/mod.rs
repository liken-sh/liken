//! How `transect` connects to the API server: HTTP/2 when the server
//! offers it, so the watches share a few connections, with keep-alive
//! pings that find a connection that died without a reset.
//!
//! kube's own client offers only HTTP/1.1, where each watch holds a
//! connection of its own: one per watched kind, and every CRD is
//! watched.  This client is built the way kube's custom-client example
//! builds one, from the same `Config`: its TLS, its server name, its
//! timeouts, its credentials, and its extra headers.  A kubeconfig that
//! names a proxy gets kube's own client, because that client can
//! connect through a proxy.

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

/// How long a connection may go without a frame before it is pinged.
pub const KEEP_ALIVE: Duration = Duration::from_secs(20);

/// How long a ping may wait for its answer before the connection is
/// closed, which ends every watch on it, so each lists again on a new
/// connection.
pub const KEEP_ALIVE_TIMEOUT: Duration = Duration::from_secs(10);

/// The stream limit assumed when `transect` cannot read the server's
/// limit: the least that RFC 9113 (section 6.5.2) recommends a server
/// allow.
const ASSUMED: u32 = 100;

type Connector = TimeoutConnector<HttpsConnector<HttpConnector>>;

/// A client of the API server `config` names, which opens at most
/// `most` streams on one connection when that is below what the server
/// allows.
pub async fn client(config: Config, most: Option<usize>) -> anyhow::Result<Client> {
    if config.proxy_url.is_some() {
        tracing::info!("the kubeconfig names a proxy, so each watch uses a connection of its own");
        return Ok(Client::try_from(config)?);
    }
    let connector = connector(&config)?;
    let tally = Tally::default();
    // hyper makes a request wait for a connection that is still opening
    // only when the client is set to HTTP/2 from the start.  With ALPN
    // alone, hyper reads the protocol after the connection is open, so a
    // burst of watches on a new connection, as at the start or after a
    // reset, would open one TCP connection each.  When the probe
    // negotiated h2, each client is built with HTTP/2 only.
    let (limit, alpn) = match limit::spoken(&config).await {
        Ok(Spoken::Http1) => {
            tracing::info!(
                "the API server uses HTTP/1.1 here, so each watch uses a connection of its own"
            );
            tally.clone().log(|| None);
            return layered(
                &config,
                tally.around(hyper_client(connector, Alpn::Offered)),
            );
        }
        Ok(Spoken::Http2(Some(limit))) => {
            tracing::info!(
                streams = limit,
                "the API server allows this many streams on a connection"
            );
            (limit, Alpn::Http2)
        }
        Ok(Spoken::Http2(None)) => {
            tracing::info!(assumed = ASSUMED, "the API server set no stream limit");
            (ASSUMED, Alpn::Http2)
        }
        Err(error) => {
            tracing::warn!(%error, assumed = ASSUMED, "cannot read the API server's stream limit");
            (ASSUMED, Alpn::Offered)
        }
    };
    let most = match most {
        Some(asked) => asked.min(self::most(limit)),
        None => self::most(limit),
    };
    tracing::info!(most, "opening at most this many streams on one connection");
    let spread = Spread::new(most, move || hyper_client(connector.clone(), alpn));
    let streams = spread.clone();
    tally.clone().log(move || Some(streams.open()));
    layered(&config, tally.around(spread))
}

/// A client that sends each request through `connections` with the
/// layers kube's own client puts above its connections, in kube's
/// order: the server's address, a retry of a 429, 503, or 504 that
/// waits for the time `Retry-After` gives, the credentials, and the
/// extra headers.
/// Without the retry, a one-shot request such as discovery would fail
/// on the first 429 that API Priority and Fairness answers a burst
/// with.
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

/// The most streams `transect` opens on one connection: a fifth below
/// the server's limit, because a stream the client has closed may
/// still count against it for a moment.
fn most(limit: u32) -> usize {
    (limit - limit / 5).max(1) as usize
}

/// The connector kube's own client builds, with HTTP/2 offered beside
/// HTTP/1.1.
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
    /// HTTP/2 from the start, for a server whose handshake chose h2.
    Http2,
    /// Whichever the TLS handshake chooses, HTTP/2 or HTTP/1.1.
    Offered,
}

/// One connection's client: one HTTP/2 connection, pinged when quiet,
/// or a pool of HTTP/1.1 connections when the server supports only
/// that.
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
