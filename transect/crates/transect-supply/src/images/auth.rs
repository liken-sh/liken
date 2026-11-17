//! How transect responds when a registry refuses a request: which
//! credential transect sends to which host, and what transect reads from
//! a token service's answer.
//!
//! A credential belongs to one registry host.  A registry often serves a
//! blob by a redirect to object storage on another host, and that host
//! can answer with a challenge of its own that names any realm.  So
//! transect answers a challenge only when it came from the host that
//! transect requested.  transect requests a token from a realm only over
//! https, so a credential sent to the realm is never sent unencrypted.

use std::time::Duration;

use reqwest::Url;
use serde::Deserialize;

use super::Failure;
use super::challenge::Challenge;
use super::credentials::Credential;

/// How long transect keeps a token when its registry gives no lifetime,
/// and how long transect keeps a credential that it sends as Basic.
pub const TOKEN_LIFETIME: Duration = Duration::from_secs(60);

#[derive(Clone)]
pub enum Auth {
    Bearer(String),
    Basic(Credential),
}

/// What to try after a refusal.
#[derive(Debug, PartialEq, Eq)]
pub enum Step {
    /// Request a token from the challenge's realm, with or without the
    /// credential.
    Token {
        with_credential: bool,
    },
    /// Send the credential itself, to a registry that requests Basic.
    Basic,
    Fail(Failure),
}

/// What a request has tried so far.
#[derive(Clone, Copy, Debug, Default)]
pub struct Tried {
    pub anonymous_token: bool,
    pub credential: bool,
}

/// The next step after a refusal with `status`, given the last
/// challenge, what the request has tried, and whether transect has a
/// credential for the host.  A registry answers an anonymous client with
/// 403 or 429 when it requires the client to identify itself, so
/// transect sends the credential after those statuses too.
pub fn next(status: u16, challenge: Option<&Challenge>, tried: Tried, credential: bool) -> Step {
    let refused = match tried.credential {
        true => Failure::Refused,
        false => Failure::NeedsCredentials,
    };
    let may_send = credential && !tried.credential;
    match (status, challenge) {
        (401, Some(Challenge::Bearer { .. })) if !tried.anonymous_token && !tried.credential => {
            Step::Token {
                with_credential: false,
            }
        }
        (401 | 403 | 429, Some(Challenge::Bearer { .. })) if may_send => Step::Token {
            with_credential: true,
        },
        (401 | 403 | 429, Some(Challenge::Basic) | None) if may_send => Step::Basic,
        (429, _) => Step::Fail(Failure::RateLimited),
        _ => Step::Fail(refused),
    }
}

/// Whether transect may answer a refusal from `answered` with the
/// credential of `requested`'s host, which requires the same scheme,
/// host, and port.  A refusal from any other address came after a
/// redirect away from the registry.
pub fn heeded(requested: &Url, answered: &Url) -> bool {
    requested.scheme() == answered.scheme()
        && requested.host_str() == answered.host_str()
        && requested.port_or_known_default() == answered.port_or_known_default()
}

#[derive(Deserialize)]
struct Token {
    #[serde(default)]
    token: Option<String>,
    #[serde(default)]
    access_token: Option<String>,
    #[serde(default)]
    expires_in: Option<u64>,
}

/// Returns the token in a realm's answer with `status` and `body`, and
/// the token's lifetime.  It returns `None` when the realm refuses to
/// give a token, and `next` then treats the result as a refusal of the
/// request.  Any other answer is `NoToken`, whatever its body contains.
pub fn granted(status: u16, body: &[u8]) -> Result<Option<(Auth, Duration)>, Failure> {
    match status {
        401 | 403 => return Ok(None),
        200..=299 => {}
        _ => return Err(Failure::NoToken),
    }
    let token: Token = serde_json::from_slice(body).map_err(|_| Failure::NoToken)?;
    let value = token.token.or(token.access_token);
    let value = value.filter(|value| !value.is_empty());
    let value = value.ok_or(Failure::NoToken)?;
    let lifetime = token.expires_in.map_or(TOKEN_LIFETIME, Duration::from_secs);
    Ok(Some((Auth::Bearer(value), lifetime)))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn bearer() -> Option<Challenge> {
        Some(Challenge::Bearer {
            realm: "https://registry.example/token".into(),
            service: None,
            scope: None,
        })
    }

    const NOTHING: Tried = Tried {
        anonymous_token: false,
        credential: false,
    };
    const ANONYMOUS: Tried = Tried {
        anonymous_token: true,
        credential: false,
    };
    const BOTH: Tried = Tried {
        anonymous_token: true,
        credential: true,
    };

    #[rstest]
    #[case::first_an_anonymous_token(401, bearer(), NOTHING, true, Step::Token { with_credential: false })]
    #[case::then_the_credential(401, bearer(), ANONYMOUS, true, Step::Token { with_credential: true })]
    #[case::no_credential_on_hand(
        401,
        bearer(),
        ANONYMOUS,
        false,
        Step::Fail(Failure::NeedsCredentials)
    )]
    #[case::the_credential_refused(401, bearer(), BOTH, true, Step::Fail(Failure::Refused))]
    #[case::forbidden_to_an_anonymous_reader(403, bearer(), ANONYMOUS, true, Step::Token { with_credential: true })]
    #[case::forbidden_without_a_credential(
        403,
        bearer(),
        ANONYMOUS,
        false,
        Step::Fail(Failure::NeedsCredentials)
    )]
    #[case::basic(401, Some(Challenge::Basic), NOTHING, true, Step::Basic)]
    #[case::basic_without_a_credential(
        401,
        Some(Challenge::Basic),
        NOTHING,
        false,
        Step::Fail(Failure::NeedsCredentials)
    )]
    #[case::rate_limited_anonymously(429, bearer(), ANONYMOUS, true, Step::Token { with_credential: true })]
    #[case::rate_limited(429, bearer(), BOTH, true, Step::Fail(Failure::RateLimited))]
    #[case::rate_limited_with_nothing_to_send(
        429,
        None,
        NOTHING,
        false,
        Step::Fail(Failure::RateLimited)
    )]
    fn answers_a_refusal(
        #[case] status: u16,
        #[case] challenge: Option<Challenge>,
        #[case] tried: Tried,
        #[case] credential: bool,
        #[case] expected: Step,
    ) {
        assert_eq!(
            next(status, challenge.as_ref(), tried, credential),
            expected
        );
    }

    #[rstest]
    #[case::the_registry_itself("https://registry.example/v2/a/blobs/x", true)]
    #[case::another_path_on_it("https://registry.example/v2/b/manifests/y", true)]
    #[case::object_storage_after_a_redirect("https://storage.example/blob?sig=1", false)]
    #[case::a_subdomain("https://cdn.registry.example/v2/a/blobs/x", false)]
    #[case::another_port("https://registry.example:8443/v2/a/blobs/x", false)]
    #[case::plain_http("http://registry.example/v2/a/blobs/x", false)]
    fn answers_only_a_refusal_from_the_host_asked(#[case] answered: &str, #[case] expected: bool) {
        let requested = Url::parse("https://registry.example/v2/a/blobs/x").unwrap();
        let answered = Url::parse(answered).unwrap();
        assert_eq!(heeded(&requested, &answered), expected);
    }

    fn token(status: u16, body: &str) -> Result<Option<String>, Failure> {
        let found = granted(status, body.as_bytes())?;
        Ok(found.map(|(auth, _)| match auth {
            Auth::Bearer(token) => token,
            Auth::Basic(_) => String::new(),
        }))
    }

    #[rstest]
    #[case::a_token(200, r#"{"token": "t"}"#, Ok(Some("t".into())))]
    #[case::an_access_token(200, r#"{"access_token": "t"}"#, Ok(Some("t".into())))]
    #[case::refused(401, "", Ok(None))]
    #[case::forbidden(403, "", Ok(None))]
    #[case::an_internal_page(200, "<html>admin</html>", Err(Failure::NoToken))]
    #[case::json_without_a_token(200, r#"{"status": "ok"}"#, Err(Failure::NoToken))]
    #[case::an_empty_token(200, r#"{"token": ""}"#, Err(Failure::NoToken))]
    #[case::not_found(404, "", Err(Failure::NoToken))]
    #[case::a_server_error(500, "", Err(Failure::NoToken))]
    #[case::a_redirect(302, "", Err(Failure::NoToken))]
    fn says_nothing_of_a_realm_but_its_token(
        #[case] status: u16,
        #[case] body: &str,
        #[case] expected: Result<Option<String>, Failure>,
    ) {
        assert_eq!(token(status, body), expected);
    }
}
