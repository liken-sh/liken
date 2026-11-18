//! Reads an image's labels from its registry over the OCI distribution
//! API: the manifest by digest, the platform's manifest when that is an
//! index, and the config blob it names.
//!
//! The first request to a repository uses the cached token or
//! credential for it, if one has not expired, and is anonymous
//! otherwise.  After a refusal, `auth::next` picks what to try: an
//! anonymous token from the realm in the registry's challenge, then the
//! registry's credential, sent as Basic or in a token request.  transect
//! sends a credential only after a refusal, and only when it has one for
//! that registry.
//!
//! A registry often serves a blob through a redirect to object storage
//! on another host.  reqwest drops the `Authorization` header on a
//! redirect to another host, so the registry's token never reaches the
//! storage host.  A refusal from the storage host fails the read
//! (`auth::same_origin`), so transect never sends a credential in reply.
//!
//! Each body is requested by its digest and is returned only when its
//! hash equals that digest, so the content that transect returns for a
//! digest is the content the digest names.
//!
//! When a host has a mirror, transect reads from the mirror instead,
//! using the mirror's credentials.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use reqwest::Url;
use reqwest::header::{ACCEPT, WWW_AUTHENTICATE};

use super::auth::{self, Auth, Step, TOKEN_LIFETIME, Tried};
use super::challenge::{self, Challenge};
use super::credentials::{Credential, Credentials};
use super::digest;
use super::manifest::{self, Manifest};
use super::mirror::{self, Mirror, Mirrors, RegistryMirror};
use super::{Failure, ImageRef, Provenance, Want, api_server, labels};
use transect_resources::resources::Field;

/// The longest one request may take.  A registry that stops responding
/// holds one of the `task::CONCURRENT` reads for at most this long.
const TIMEOUT: Duration = Duration::from_secs(15);
/// The most bytes transect reads from one response.  A manifest or a
/// config blob is a few kilobytes, so a much larger response is
/// something else.
const MAX_BODY: usize = 4 << 20;

pub struct Registries {
    http: reqwest::Client,
    credentials: Credentials,
    /// The mirrors set by command-line flags, which take precedence over
    /// the `RegistryMirror` resources.
    flags: Mirrors,
    /// The mirrors that `RegistryMirror` resources declare.
    resources: Option<Field<Vec<RegistryMirror>>>,
    /// The Kubernetes client, used when a mirror is the API server
    /// itself.
    api: Option<kube::Client>,
    /// The cached authentication for each (registry, repository), and
    /// when it expires: a token as soon as the realm issues it, or a
    /// credential after a request with it as Basic succeeds.
    tokens: Mutex<HashMap<(String, String), (Auth, Instant)>>,
}

impl Registries {
    pub fn new(credentials: Credentials) -> anyhow::Result<Self> {
        let http = reqwest::Client::builder()
            .timeout(TIMEOUT)
            .user_agent(concat!("transect/", env!("CARGO_PKG_VERSION")))
            .build()?;
        Ok(Self {
            http,
            credentials,
            flags: Mirrors::new(),
            resources: None,
            api: None,
            tokens: Mutex::default(),
        })
    }

    /// Returns these registries set to read each host that `flags` or
    /// the `RegistryMirror` resources name from its mirror.  A mirror
    /// at the API server's address is read through `api`.
    pub fn with_mirrors(
        self,
        flags: Mirrors,
        resources: Field<Vec<RegistryMirror>>,
        api: kube::Client,
    ) -> Self {
        Self {
            flags,
            resources: Some(resources),
            api: Some(api),
            ..self
        }
    }

    /// The mirror of each host, from the flags and the current
    /// resources.
    pub fn mirrors(&self) -> Mirrors {
        let resources = self.resources.as_ref().map(Field::read);
        mirror::merged(&self.flags, resources.as_deref().unwrap_or_default())
    }

    /// The labels of the image `want` names, for its platform.  The
    /// image's digest may name an index, and then the manifest for the
    /// platform is read from it.
    pub async fn provenance(&self, want: &Want) -> Result<Provenance, Failure> {
        let image = &want.image;
        let manifest = |digest: String| async move {
            let body = self
                .content(image, "manifests", &digest, manifest::ACCEPT)
                .await?;
            manifest::parse(&body)
        };
        let config = match manifest(image.digest.clone()).await? {
            Manifest::Image { config } => config,
            Manifest::Index(entries) => {
                let child = manifest::select(&entries, &want.platform)
                    .ok_or_else(|| Failure::NoPlatform(want.platform.to_string()))?;
                match manifest(child.to_string()).await? {
                    Manifest::Image { config } => config,
                    Manifest::Index(_) => return Err(Failure::Unsupported),
                }
            }
        };
        let blob = self.content(image, "blobs", &config, "*/*").await?;
        labels::provenance(&blob)
    }

    /// Returns the manifest or blob that `digest` names under `kind`,
    /// only when the body hashes to `digest`.  It validates `digest`
    /// before putting it in the request path, because a digest read from
    /// a manifest is text that the registry chose.
    async fn content(
        &self,
        image: &ImageRef,
        kind: &str,
        digest: &str,
        accept: &str,
    ) -> Result<Vec<u8>, Failure> {
        if !digest::ok(digest) {
            return Err(Failure::Malformed);
        }
        let body = self.get(image, &format!("{kind}/{digest}"), accept).await?;
        match digest::matches(digest, &body) {
            true => Ok(body),
            false => Err(Failure::Mismatch),
        }
    }

    /// The body at `path` under the image's repository, from the
    /// registry or from its mirror.
    async fn get(&self, image: &ImageRef, path: &str, accept: &str) -> Result<Vec<u8>, Failure> {
        let repository = &image.repository;
        match self.mirrors().get(&image.registry) {
            None => {
                let url = format!("https://{}/v2/{repository}/{path}", image.api_host());
                self.get_from(&url, &image.registry, image, accept).await
            }
            Some(Mirror::ApiServer) => match &self.api {
                Some(api) => api_server::get(api, image, path, accept).await,
                None => Err(Failure::Unreachable),
            },
            Some(mirror @ Mirror::Url(base)) => {
                let url = format!("{base}/v2/{repository}/{path}?ns={}", image.registry);
                let host = mirror.host().unwrap_or(&image.registry);
                self.get_from(&url, host, image, accept).await
            }
        }
    }

    /// Returns the body at `url`.  It responds to each authentication
    /// challenge with the step that `auth::next` chooses, and sends the
    /// credential of `registry` only when that step calls for it.
    async fn get_from(
        &self,
        url: &str,
        registry: &str,
        image: &ImageRef,
        accept: &str,
    ) -> Result<Vec<u8>, Failure> {
        let requested = Url::parse(url).map_err(|_| Failure::Malformed)?;
        let credential = self.credentials.of(registry);
        let key = (registry.to_string(), image.repository.clone());
        let mut auth = self.cached(&key);
        let mut tried = Tried::default();
        let mut last: Option<Challenge> = None;
        // Each refusal moves to a form of authentication that `tried`
        // does not record yet, and `auth::next` fails when none is left,
        // so the loop ends after a few requests.
        for _ in 0..4 {
            let response = self.send(url, accept, auth.as_ref()).await?;
            let status = response.status().as_u16();
            if response.status().is_success() {
                if let Some(basic @ Auth::Basic(_)) = auth {
                    self.keep(key, basic, TOKEN_LIFETIME);
                }
                return read(response).await;
            }
            let same_origin = auth::same_origin(&requested, response.url());
            if !matches!(status, 401 | 403 | 429) || !same_origin {
                return Err(failure(status, response).await);
            }
            let challenge = response.headers().get(WWW_AUTHENTICATE);
            let challenge = challenge
                .and_then(|value| value.to_str().ok())
                .and_then(challenge::parse);
            last = challenge.or(last);
            auth = None;
            while auth.is_none() {
                match auth::next(status, last.as_ref(), tried, credential.is_some()) {
                    Step::Fail(failure) => return Err(failure),
                    Step::Basic => {
                        tried.credential = true;
                        auth = credential.cloned().map(Auth::Basic);
                    }
                    Step::Token { with_credential } => {
                        let Some(Challenge::Bearer {
                            realm,
                            service,
                            scope,
                        }) = &last
                        else {
                            return Err(Failure::NeedsCredentials);
                        };
                        let scope = scope
                            .clone()
                            .unwrap_or_else(|| format!("repository:{}:pull", image.repository));
                        let sent = credential.filter(|_| with_credential);
                        match with_credential {
                            true => tried.credential = true,
                            false => tried.anonymous_token = true,
                        }
                        auth = self
                            .token(realm, service.as_deref(), &scope, sent, &key)
                            .await?;
                    }
                }
            }
        }
        Err(Failure::Refused)
    }

    async fn send(
        &self,
        url: &str,
        accept: &str,
        auth: Option<&Auth>,
    ) -> Result<reqwest::Response, Failure> {
        let request = self.http.get(url).header(ACCEPT, accept);
        let request = match auth {
            Some(Auth::Bearer(token)) => request.bearer_auth(token),
            Some(Auth::Basic(credential)) => {
                request.basic_auth(&credential.username, Some(&credential.password))
            }
            None => request,
        };
        request.send().await.map_err(|_| Failure::Unreachable)
    }

    /// Requests a token from `realm`.  It returns `None` when the realm
    /// refuses to issue one, and `auth::next` then treats the original
    /// request as refused.  The realm is an address that the registry's
    /// challenge supplied, so nothing from its response except the token
    /// is passed on to a page.
    async fn token(
        &self,
        realm: &str,
        service: Option<&str>,
        scope: &str,
        credential: Option<&Credential>,
        key: &(String, String),
    ) -> Result<Option<Auth>, Failure> {
        let mut query = vec![("scope", scope)];
        query.extend(service.map(|service| ("service", service)));
        let request = self.http.get(realm).query(&query);
        let request = match credential {
            Some(credential) => {
                request.basic_auth(&credential.username, Some(&credential.password))
            }
            None => request,
        };
        let response = request.send().await.map_err(|_| Failure::NoToken)?;
        let status = response.status().as_u16();
        let body = match response.status().is_success() {
            true => read(response).await.map_err(|_| Failure::NoToken)?,
            false => Vec::new(),
        };
        let Some((auth, lifetime)) = auth::granted(status, &body)? else {
            return Ok(None);
        };
        self.keep(key.clone(), auth.clone(), lifetime);
        Ok(Some(auth))
    }

    fn cached(&self, key: &(String, String)) -> Option<Auth> {
        let tokens = self
            .tokens
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let (auth, until) = tokens.get(key)?;
        (Instant::now() < *until).then(|| auth.clone())
    }

    /// Caches `auth` for the repository until 10 seconds before its
    /// lifetime ends, so the next read of the repository does not repeat
    /// the refused request and the token request.
    fn keep(&self, key: (String, String), auth: Auth, lifetime: Duration) {
        let lifetime = lifetime.saturating_sub(Duration::from_secs(10));
        if lifetime.is_zero() {
            return;
        }
        let mut tokens = self
            .tokens
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        tokens.insert(key, (auth, Instant::now() + lifetime));
    }
}

/// The body of a response, or `TooLarge` when it exceeds `MAX_BODY`.
async fn read(mut response: reqwest::Response) -> Result<Vec<u8>, Failure> {
    let mut body = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| Failure::Unreachable)? {
        body.extend_from_slice(&chunk);
        if body.len() > MAX_BODY {
            return Err(Failure::TooLarge);
        }
    }
    Ok(body)
}

/// The failure for a response that is not a success and not an
/// authentication challenge.  A registry responds to a digest it does
/// not have with a 404 and a JSON `errors` body, while a host that is
/// not a registry returns its own 404 page, so the two get different
/// failures.
async fn failure(status: u16, response: reqwest::Response) -> Failure {
    if status != 404 {
        return Failure::Status(status);
    }
    let api = response
        .headers()
        .contains_key("docker-distribution-api-version");
    let body = read(response).await.unwrap_or_default();
    let errors = serde_json::from_slice::<serde_json::Value>(&body)
        .ok()
        .is_some_and(|value| value.get("errors").is_some());
    match api || errors {
        true => Failure::NotFound,
        false => Failure::NotARegistry,
    }
}
