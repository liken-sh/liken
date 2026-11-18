//! Reads the stream limit an API server announces in its HTTP/2
//! `SETTINGS_MAX_CONCURRENT_STREAMS` setting, which kube-apiserver sets
//! with `--http2-max-streams-per-connection`.
//!
//! hyper does not expose the settings its peer sends. So at startup
//! `transect` opens a connection of its own, with the same TLS
//! configuration as its client, sends the HTTP/2 preface, and reads the
//! SETTINGS frame that a server must send first. It sends no request, so
//! it needs no credentials.

use std::sync::Arc;
use std::time::Duration;

use anyhow::Context;
use kube::Config;
use kube::client::ConfigExt;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio_rustls::TlsConnector;
use tokio_rustls::rustls::pki_types::ServerName;

/// How long the probe may take, from the TCP connect to the server's
/// SETTINGS frame.
const ATTEMPT: Duration = Duration::from_secs(5);

/// What an HTTP/2 client sends first: the connection preface, then an
/// empty SETTINGS frame.
const PREFACE: &[u8] = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n\x00\x00\x00\x04\x00\x00\x00\x00\x00";

/// The HTTP/2 frame type for SETTINGS, and the identifier of the
/// maximum concurrent streams setting.
const SETTINGS: u8 = 0x4;
const MAX_CONCURRENT_STREAMS: u16 = 0x3;

/// The protocol that `transect` uses with the API server.
#[derive(Debug, PartialEq)]
pub enum Spoken {
    /// HTTP/1.1. A plain `http://` URL, such as the one `kubectl proxy`
    /// serves, always uses it, and so does a server that does not agree
    /// to `h2` in the TLS handshake.
    Http1,
    /// HTTP/2, with the stream limit the server sent, or `None` when it
    /// sent no limit.
    Http2(Option<u32>),
}

/// Finds out which protocol the API server in `config` uses, and its
/// stream limit when it uses HTTP/2.
pub async fn spoken(config: &Config) -> anyhow::Result<Spoken> {
    if config.cluster_url.scheme_str() == Some("http") {
        return Ok(Spoken::Http1);
    }
    tokio::time::timeout(ATTEMPT, read(config))
        .await
        .context("waiting for the SETTINGS frame of the API server")?
}

async fn read(config: &Config) -> anyhow::Result<Spoken> {
    let url = &config.cluster_url;
    let host = url.host().context("the cluster URL has no host")?;
    let port = url.port_u16().unwrap_or(443);
    let mut tls = config.rustls_client_config()?;
    tls.alpn_protocols = vec![b"h2".to_vec()];
    let name = config.tls_server_name.as_deref().unwrap_or(host);
    let name = ServerName::try_from(name.trim_matches(['[', ']']).to_string())?;
    let tcp = tokio::net::TcpStream::connect((host.trim_matches(['[', ']']), port)).await?;
    let mut stream = TlsConnector::from(Arc::new(tls)).connect(name, tcp).await?;
    if stream.get_ref().1.alpn_protocol() != Some(b"h2") {
        return Ok(Spoken::Http1);
    }
    stream.write_all(PREFACE).await?;
    let mut frame = Vec::new();
    let mut chunk = [0u8; 4096];
    loop {
        if let Some(settings) = settings(&frame) {
            return Ok(Spoken::Http2(max_streams(settings)));
        }
        let read = stream.read(&mut chunk).await?;
        anyhow::ensure!(
            read > 0,
            "the API server closed the connection before it sent a SETTINGS frame"
        );
        frame.extend_from_slice(&chunk[..read]);
    }
}

/// Returns the payload of the SETTINGS frame at the start of `bytes`,
/// or `None` when the frame is another type or has not fully arrived.
fn settings(bytes: &[u8]) -> Option<&[u8]> {
    let header = bytes.get(..9)?;
    let length = u32::from_be_bytes([0, header[0], header[1], header[2]]) as usize;
    (header[3] == SETTINGS).then_some(())?;
    bytes.get(9..9 + length)
}

/// Returns the maximum concurrent streams in `settings`, which holds
/// six-byte entries of a two-byte identifier and a four-byte value.
fn max_streams(settings: &[u8]) -> Option<u32> {
    let (each, _) = settings.as_chunks::<6>();
    each.iter().find_map(|setting| {
        let id = u16::from_be_bytes([setting[0], setting[1]]);
        let value = u32::from_be_bytes([setting[2], setting[3], setting[4], setting[5]]);
        (id == MAX_CONCURRENT_STREAMS).then_some(value)
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// Builds a frame of type `kind` with `settings` as its payload,
    /// followed by `extra` bytes.
    fn frame(kind: u8, settings: &[(u16, u32)], extra: &[u8]) -> Vec<u8> {
        let payload: Vec<u8> = settings
            .iter()
            .flat_map(|(id, value)| [id.to_be_bytes().as_slice(), &value.to_be_bytes()].concat())
            .collect();
        let length = (payload.len() as u32).to_be_bytes();
        let mut bytes = vec![length[1], length[2], length[3], kind, 0, 0, 0, 0, 0];
        bytes.extend(payload);
        bytes.extend(extra);
        bytes
    }

    #[rstest]
    #[case::a_limit(frame(SETTINGS, &[(5, 16384), (3, 250), (4, 65535)], &[]), Some(Some(250)))]
    #[case::no_limit(frame(SETTINGS, &[(5, 16384)], &[]), Some(None))]
    #[case::with_the_next_frame(frame(SETTINGS, &[(3, 100)], &[0, 0, 0, 4, 1]), Some(Some(100)))]
    #[case::not_all_here(frame(SETTINGS, &[(3, 100)], &[])[..12].to_vec(), None)]
    #[case::another_frame(frame(0x7, &[(3, 100)], &[]), None)]
    fn reads_the_stream_limit_from_the_first_frame(
        #[case] bytes: Vec<u8>,
        #[case] expected: Option<Option<u32>>,
    ) {
        assert_eq!(settings(&bytes).map(max_streams), expected);
    }

    #[rstest]
    #[tokio::test]
    async fn an_http_url_uses_http1_without_connecting() {
        // Nothing listens on port 1, so a connection attempt would fail.
        let config = Config::new("http://127.0.0.1:1".parse().unwrap());
        assert_eq!(spoken(&config).await.unwrap(), Spoken::Http1);
    }
}
