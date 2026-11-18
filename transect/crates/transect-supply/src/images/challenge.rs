//! Parses a registry's `WWW-Authenticate` challenge, which tells a client
//! how to authenticate: with a bearer token from a realm, or with a
//! username and password sent on each request.

/// The authentication that a registry requires.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Challenge {
    /// A token from `realm`, requested with `service` and `scope`.
    Bearer {
        realm: String,
        service: Option<String>,
        scope: Option<String>,
    },
    Basic,
}

/// Parses a `WWW-Authenticate` header.  Returns `None` for a scheme
/// other than Basic and Bearer, and for a Bearer challenge whose realm
/// is not an `https://` URL.  transect may send a credential to the
/// realm, so it never requests a token over plain http.
pub fn parse(header: &str) -> Option<Challenge> {
    let header = header.trim();
    let (scheme, rest) = header.split_once(' ').unwrap_or((header, ""));
    if scheme.eq_ignore_ascii_case("basic") {
        return Some(Challenge::Basic);
    }
    if !scheme.eq_ignore_ascii_case("bearer") {
        return None;
    }
    let params = params(rest);
    let get = |key: &str| {
        params
            .iter()
            .find(|(name, _)| name.eq_ignore_ascii_case(key))
            .map(|(_, value)| value.clone())
    };
    let realm = get("realm").filter(|realm| realm.starts_with("https://"))?;
    Some(Challenge::Bearer {
        realm,
        service: get("service"),
        scope: get("scope"),
    })
}

/// The `key=value` pairs of a challenge.  A quoted value may contain
/// commas, as a scope with several actions does.
fn params(text: &str) -> Vec<(String, String)> {
    let mut params = Vec::new();
    let mut chars = text.chars().peekable();
    loop {
        while chars.next_if(|c| *c == ',' || c.is_whitespace()).is_some() {}
        let key: String = std::iter::from_fn(|| chars.next_if(|c| *c != '=')).collect();
        if chars.next().is_none() {
            return params;
        }
        let value: String = match chars.peek() {
            Some('"') => {
                chars.next();
                let mut value = String::new();
                while let Some(c) = chars.next() {
                    match c {
                        '"' => break,
                        '\\' => value.extend(chars.next()),
                        c => value.push(c),
                    }
                }
                value
            }
            _ => std::iter::from_fn(|| chars.next_if(|c| *c != ',')).collect(),
        };
        params.push((key.trim().to_string(), value.trim().to_string()));
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn bearer(realm: &str, service: Option<&str>, scope: Option<&str>) -> Option<Challenge> {
        Some(Challenge::Bearer {
            realm: realm.into(),
            service: service.map(Into::into),
            scope: scope.map(Into::into),
        })
    }

    #[rstest]
    #[case::docker_hub_style(
        r#"Bearer realm="https://auth.registry.example/token",service="registry.example",scope="repository:team/app:pull""#,
        bearer("https://auth.registry.example/token", Some("registry.example"), Some("repository:team/app:pull"))
    )]
    #[case::spaces_between(
        r#"Bearer realm="https://registry.example/token", service="registry.example""#,
        bearer("https://registry.example/token", Some("registry.example"), None)
    )]
    #[case::a_scope_with_a_comma(
        r#"Bearer realm="https://registry.example/token",scope="repository:team/app:pull,push""#,
        bearer(
            "https://registry.example/token",
            None,
            Some("repository:team/app:pull,push")
        )
    )]
    #[case::unquoted(
        "bearer realm=https://registry.example/token,service=registry.example",
        bearer("https://registry.example/token", Some("registry.example"), None)
    )]
    #[case::basic(r#"Basic realm="Registry""#, Some(Challenge::Basic))]
    #[case::no_realm(r#"Bearer service="registry.example""#, None)]
    #[case::a_realm_that_is_not_a_url(r#"Bearer realm="javascript:alert(1)""#, None)]
    #[case::a_plain_http_realm(r#"Bearer realm="http://registry.example/token""#, None)]
    #[case::a_realm_with_no_scheme(r#"Bearer realm="registry.example/token""#, None)]
    #[case::another_scheme("Negotiate", None)]
    fn reads_a_challenge(#[case] header: &str, #[case] expected: Option<Challenge>) {
        assert_eq!(parse(header), expected);
    }
}
