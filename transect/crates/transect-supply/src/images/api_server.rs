//! Reads a registry that is served at the API server's address, as k3s's
//! embedded registry is.  That registry accepts only a client that
//! authenticates with credentials that the API server also accepts, so
//! transect sends the request through its Kubernetes client, with the
//! client certificate or token that the client already has.

use std::time::Duration;

use http::header::ACCEPT;

use super::{Failure, ImageRef};

/// The same limit as `TIMEOUT` in `client.rs`.
const TIMEOUT: Duration = Duration::from_secs(15);
/// The largest body that transect accepts, the same as `MAX_BODY` in
/// `client.rs`.  The Kubernetes client reads the whole body before
/// `get` checks its size, so this limit rejects a large body but does
/// not stop transect from reading it.
const MAX_BODY: usize = 4 << 20;

/// Returns the body at `path` under `image`'s repository.  The request
/// sends the image's registry host in the `ns` query parameter, as
/// containerd's requests to a mirror do.
pub async fn get(
    client: &kube::Client,
    image: &ImageRef,
    path: &str,
    accept: &str,
) -> Result<Vec<u8>, Failure> {
    let uri = format!("/v2/{}/{path}?ns={}", image.repository, image.registry);
    let request = http::Request::get(uri)
        .header(ACCEPT, accept)
        .body(Vec::new())
        .map_err(|_| Failure::Malformed)?;
    let response = tokio::time::timeout(TIMEOUT, client.request_text(request)).await;
    match response {
        Err(_) => Err(Failure::Unreachable),
        Ok(Ok(text)) if text.len() > MAX_BODY => Err(Failure::TooLarge),
        Ok(Ok(text)) => Ok(text.into_bytes()),
        Ok(Err(kube::Error::Api(status))) => Err(failure(status.code)),
        Ok(Err(_)) => Err(Failure::Unreachable),
    }
}

/// The `Failure` for a request that the API server rejected with HTTP
/// status `code`.
fn failure(code: u16) -> Failure {
    match code {
        401 => Failure::NeedsCredentials,
        403 => Failure::Refused,
        404 => Failure::NotFound,
        429 => Failure::RateLimited,
        code => Failure::Status(code),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::unauthorized(401, Failure::NeedsCredentials)]
    #[case::refused(403, Failure::Refused)]
    #[case::not_found(404, Failure::NotFound)]
    #[case::rate_limited(429, Failure::RateLimited)]
    #[case::anything_else(502, Failure::Status(502))]
    fn maps_an_api_server_status_to_a_failure(#[case] code: u16, #[case] expected: Failure) {
        assert_eq!(failure(code), expected);
    }
}
