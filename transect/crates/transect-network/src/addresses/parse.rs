//! Extracts host addresses from configuration values: a bare host, a
//! `host:port`, or a URL with a scheme, userinfo, a path, and a query.

/// Endings of the last word of a variable or flag name that mark its
/// value as a host, as in `DB_HOST`, `APP_DBHOST`, `CACHE_ADDR`, or
/// `--db-host`.
const HOST_WORDS: [&str; 10] = [
    "host", "hosts", "hostname", "addr", "address", "server", "servers", "endpoint", "broker",
    "brokers",
];

/// A host in lowercase, the port written after it, and the path of a
/// URL.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Address {
    pub host: String,
    pub port: Option<u16>,
    /// The URL's path, or `/` when it has none. It selects among the
    /// paths of an `Ingress` host.
    pub path: String,
    /// True when the value is one short name with no scheme, port, dot,
    /// or path. The same text is often something other than a host, such
    /// as a database name or a user name, so `variable` keeps it only
    /// when the variable's name indicates a host.
    pub bare: bool,
}

impl Address {
    /// The host, with `:port` when the value had a port. This leaves out
    /// the userinfo, so a password in a URL never reaches a page.
    pub fn text(&self) -> String {
        match self.port {
            Some(port) => format!("{}:{port}", self.host),
            None => self.host.clone(),
        }
    }
}

/// The addresses in the value of the environment variable `name`. A
/// bare short name counts only when `name` indicates a host.
pub fn variable(name: &str, value: &str) -> Vec<Address> {
    let mut found = addresses(value);
    if !indicates_a_host(name) {
        found.retain(|address| !address.bare);
    }
    found
}

/// The addresses in one container argument. For a flag such as
/// `--upstream=http://api:8080`, it reads the text after `=` as the
/// value of a variable named for the flag. A flag with no `=` has no
/// address. An argument that is not a flag is read with no variable
/// name, so a bare short name in it is dropped, because it is more
/// often a subcommand than a host.
pub fn argument(argument: &str) -> Vec<Address> {
    match argument.strip_prefix('-') {
        Some(flag) => flag
            .split_once('=')
            .map_or_else(Vec::new, |(name, value)| variable(name, value)),
        None => variable("", argument),
    }
}

/// Whether the last word of `name` ends with one of `HOST_WORDS`.
fn indicates_a_host(name: &str) -> bool {
    let name = name.to_ascii_lowercase();
    let last = name.rsplit(['_', '-', '.']).next().unwrap_or_default();
    HOST_WORDS.iter().any(|word| last.ends_with(word))
}

/// The addresses in `value`, one for each item of a comma-separated
/// list, as a client with several brokers or replicas is configured.
/// A value with whitespace has no address, because it is prose or a
/// shell script, and one of its words that matches a `Service` name is
/// no evidence that the workload calls that `Service`.
pub fn addresses(value: &str) -> Vec<Address> {
    let value = value.trim();
    if value.is_empty() || value.contains(char::is_whitespace) {
        return Vec::new();
    }
    value.split(',').filter_map(address).collect()
}

/// The host, port, and path of one item. The authority is the text
/// after `://`, when there is one, up to the first `/`, `?`, or `#`.
/// The host is what follows the last `@` in the authority, which skips
/// userinfo. Without a scheme, `user@host` is an email address, whose
/// domain is not a host the workload calls. It is read as an address
/// only when a `:` follows the host, as in the `git@host:owner/repo`
/// form of an SSH address, and then it has no port and the path `/`.
fn address(item: &str) -> Option<Address> {
    let (scheme, rest) = match item.split_once("://") {
        Some((_, rest)) => (true, rest),
        None => (false, item),
    };
    if !scheme && let Some((_, host)) = rest.rsplit_once('@') {
        let (host, _) = host.split_once(':')?;
        return named(host, None, "/", false);
    }
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let (authority, after) = rest.split_at(end);
    let path = match after.split(['?', '#']).next() {
        Some(path) if path.starts_with('/') => path,
        _ => "/",
    };
    let host_port = authority
        .rsplit_once('@')
        .map_or(authority, |(_, host)| host);
    let (host, port) = split_port(host_port)?;
    let bare = !scheme && port.is_none() && !host.contains(['.', ':']) && after.is_empty();
    named(host, port, path, bare)
}

fn named(host: &str, port: Option<u16>, path: &str, bare: bool) -> Option<Address> {
    // A trailing dot makes a DNS name fully qualified, and it is the same
    // name without it. DNS compares names without regard to case.
    let host = host.trim_end_matches('.').to_ascii_lowercase();
    let host_characters = |c: char| c.is_ascii_alphanumeric() || matches!(c, '-' | '.' | ':');
    if host.is_empty() || !host.chars().all(host_characters) {
        return None;
    }
    Some(Address {
        host,
        port,
        path: path.into(),
        bare,
    })
}

/// Splits a port from a host. An IPv6 address has brackets when a port
/// follows it. An unbracketed one has colons of its own and no port. A
/// port that is not a number makes the item no address, as in
/// `key:value`.
fn split_port(host_port: &str) -> Option<(&str, Option<u16>)> {
    if let Some(bracketed) = host_port.strip_prefix('[') {
        let (host, after) = bracketed.split_once(']')?;
        return match after.strip_prefix(':') {
            Some(port) => Some((host, Some(port.parse().ok()?))),
            None if after.is_empty() => Some((host, None)),
            None => None,
        };
    }
    match host_port.split_once(':') {
        Some((host, port)) if !port.contains(':') => Some((host, Some(port.parse().ok()?))),
        _ => Some((host_port, None)),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn at(host: &str, port: Option<u16>) -> Address {
        Address {
            host: host.into(),
            port,
            path: "/".into(),
            bare: false,
        }
    }

    fn bare(host: &str) -> Address {
        Address {
            bare: true,
            ..at(host, None)
        }
    }

    fn on(path: &str, address: Address) -> Address {
        Address {
            path: path.into(),
            ..address
        }
    }

    #[rstest]
    #[case::a_bare_host("db", vec![bare("db")])]
    #[case::a_host_and_port("db:5432", vec![at("db", Some(5432))])]
    #[case::upper_case("DB.Shop", vec![at("db.shop", None)])]
    #[case::fully_qualified("db.shop.svc.cluster.local.", vec![at("db.shop.svc.cluster.local", None)])]
    #[case::a_url("http://api.shop:8080/v1/items", vec![on("/v1/items", at("api.shop", Some(8080)))])]
    #[case::a_url_with_a_short_name("http://api", vec![at("api", None)])]
    #[case::a_url_with_userinfo(
        "postgres://app:p@ss@db.shop.svc:5432/app?sslmode=disable",
        vec![on("/app", at("db.shop.svc", Some(5432)))]
    )]
    #[case::a_url_with_a_query_and_no_path("redis://cache?db=0", vec![at("cache", None)])]
    #[case::a_url_with_a_fragment("https://web#top", vec![at("web", None)])]
    #[case::a_jdbc_url("jdbc:postgresql://db:5432/app", vec![on("/app", at("db", Some(5432)))])]
    #[case::a_host_and_path("cache:6379/0", vec![on("/0", at("cache", Some(6379)))])]
    #[case::a_list("queue-0:9092,queue-1:9092", vec![at("queue-0", Some(9092)), at("queue-1", Some(9092))])]
    #[case::an_address("10.0.0.5:80", vec![at("10.0.0.5", Some(80))])]
    #[case::a_bracketed_ipv6("[fd00::5]:80", vec![at("fd00::5", Some(80))])]
    #[case::a_bare_ipv6("fd00::5", vec![at("fd00::5", None)])]
    #[case::an_ssh_address("git@code.example.com:owner/repo.git", vec![at("code.example.com", None)])]
    #[case::an_email_address("admin@example.com", vec![])]
    #[case::prose("talk to the db", vec![])]
    #[case::a_path("/var/lib/data", vec![])]
    #[case::a_key_and_value("mode:fast", vec![])]
    #[case::empty("", vec![])]
    #[case::a_template("http://$(HOST):80", vec![])]
    fn reads_the_addresses_a_value_names(#[case] value: &str, #[case] expected: Vec<Address>) {
        assert_eq!(addresses(value), expected);
    }

    #[rstest]
    #[case::a_host("DB_HOST", "db", vec![bare("db")])]
    #[case::a_host_run_together("APP_DBHOST", "db", vec![bare("db")])]
    #[case::an_address("CACHE_ADDR", "cache", vec![bare("cache")])]
    #[case::a_server_in_lower_case("mail.server", "mail", vec![bare("mail")])]
    #[case::a_name("DB_NAME", "db", vec![])]
    #[case::a_server_name("MCP_SERVER_NAME", "db", vec![])]
    #[case::a_user("USER", "db", vec![])]
    #[case::a_qualified_name_under_any_name("DB_NAME", "db.shop", vec![at("db.shop", None)])]
    #[case::a_port_under_any_name("UPSTREAM", "db:5432", vec![at("db", Some(5432))])]
    fn a_bare_name_counts_where_the_variable_indicates_a_host(
        #[case] name: &str,
        #[case] value: &str,
        #[case] expected: Vec<Address>,
    ) {
        assert_eq!(variable(name, value), expected);
    }

    #[rstest]
    #[case::a_flags_url("--upstream=http://api:8080", vec![at("api", Some(8080))])]
    #[case::a_flag_that_indicates_a_host("--db-host=db", vec![bare("db")])]
    #[case::a_flag_that_lists_names("--dns-names=db", vec![])]
    #[case::a_flag_alone("--verbose", vec![])]
    #[case::a_value("cache:6379", vec![at("cache", Some(6379))])]
    #[case::a_subcommand("serve", vec![])]
    fn reads_the_addresses_an_argument_names(#[case] value: &str, #[case] expected: Vec<Address>) {
        assert_eq!(argument(value), expected);
    }

    #[rstest]
    #[case::with_a_port(at("db", Some(5432)), "db:5432")]
    #[case::without(on("/app", at("db.shop", None)), "db.shop")]
    fn shows_only_the_host_and_port(#[case] address: Address, #[case] text: &str) {
        assert_eq!(address.text(), text);
    }
}
