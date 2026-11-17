//! Reads a registry that the API server's address also serves, as k3s's
//! embedded registry does, through transect's own client for the API.
//! That registry answers only a client that authenticates to the cluster
//! with the same credentials that the API accepts.  So transect sends the
//! request on its API connection, with the certificate or token that
//! transect was given.

use std::time::Duration;

use http::header::ACCEPT;

use super::{Failure, ImageRef};

/// The longest one request may take, the same limit as for a registry.
const TIMEOUT: Duration = Duration::from_secs(15);
/// The most bytes transect reads of one answer, the same limit as for a
/// registry.
const MAX_BODY: usize = 4 << 20;

/// Returns the body at `path` under `image`'s repository.  transect
/// requests it from the API server with the image's host in the `ns`
/// query parameter, as containerd does when it requests from a mirror.
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
    let answer = tokio::time::timeout(TIMEOUT, client.request_text(request)).await;
    match answer {
        Err(_) => Err(Failure::Unreachable),
        Ok(Ok(text)) if text.len() > MAX_BODY => Err(Failure::TooLarge),
        Ok(Ok(text)) => Ok(text.into_bytes()),
        Ok(Err(kube::Error::Api(status))) => Err(failure(status.code)),
        Ok(Err(_)) => Err(Failure::Unreachable),
    }
}

/// Why a request the API server answered with `code` failed.
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
    #[case::unknown_to_the_cluster(401, Failure::NeedsCredentials)]
    #[case::refused(403, Failure::Refused)]
    #[case::not_held(404, Failure::NotFound)]
    #[case::too_often(429, Failure::RateLimited)]
    #[case::anything_else(502, Failure::Status(502))]
    fn reads_why_the_api_server_refused(#[case] code: u16, #[case] expected: Failure) {
        assert_eq!(failure(code), expected);
    }
}
