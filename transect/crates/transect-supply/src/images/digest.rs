//! A content digest, in the form that an image reference and a manifest
//! use: `sha256:` or `sha512:` followed by the hash in hex.
//!
//! transect puts a digest into a request's path.  A digest that is
//! anything else, such as `../` in a manifest a registry wrote, would
//! make transect request another path on the same host with the same
//! credentials.  transect also keeps labels by digest for as long as it
//! runs, so it keeps a body only when the body hashes to the digest that
//! transect requested.  A mirror that answers with other content then
//! cannot attach that content's labels to an image.

use ring::digest::{SHA256, SHA512, digest};

/// Whether `digest` is a sha256 or sha512 digest, with nothing else in
/// it.
pub fn ok(digest: &str) -> bool {
    let Some((algorithm, hex)) = digest.split_once(':') else {
        return false;
    };
    let length = match algorithm {
        "sha256" => 64,
        "sha512" => 128,
        _ => return false,
    };
    hex.len() == length && hex.chars().all(|c| c.is_ascii_hexdigit())
}

/// Whether `body` hashes to `expected`.  A digest that is not `ok`
/// matches nothing.
pub fn matches(expected: &str, body: &[u8]) -> bool {
    if !ok(expected) {
        return false;
    }
    let (algorithm, hex) = expected.split_once(':').unwrap_or_default();
    let algorithm = match algorithm {
        "sha256" => &SHA256,
        _ => &SHA512,
    };
    let found = digest(algorithm, body);
    let found: String = found.as_ref().iter().map(|b| format!("{b:02x}")).collect();
    found.eq_ignore_ascii_case(hex)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// The sha256 of the empty body.
    const EMPTY: &str = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";

    #[rstest]
    #[case::sha256(EMPTY, true)]
    #[case::sha512(&format!("sha512:{}", "a".repeat(128)), true)]
    #[case::upper_case_hex(&EMPTY.to_uppercase().replace("SHA256", "sha256"), true)]
    #[case::a_path(&format!("sha256:{}/../../api/v1/secrets", "a".repeat(64)), false)]
    #[case::a_path_alone("../../api/v1/namespaces", false)]
    #[case::too_short("sha256:abc", false)]
    #[case::not_hex(&format!("sha256:{}", "g".repeat(64)), false)]
    #[case::another_algorithm(&format!("md5:{}", "a".repeat(32)), false)]
    #[case::a_query(&format!("sha256:{}?x=1", "a".repeat(64)), false)]
    #[case::empty("", false)]
    fn takes_only_a_digest(#[case] digest: &str, #[case] expected: bool) {
        assert_eq!(ok(digest), expected);
    }

    #[rstest]
    #[case::the_content_it_names(EMPTY, b"", true)]
    #[case::other_content(EMPTY, b"{}", false)]
    #[case::a_digest_of_another_length(&format!("sha512:{}", "e".repeat(128)), b"", false)]
    #[case::no_digest("../config", b"", false)]
    fn keeps_a_body_only_when_it_hashes_to_its_digest(
        #[case] digest: &str,
        #[case] body: &[u8],
        #[case] expected: bool,
    ) {
        assert_eq!(matches(digest, body), expected);
    }
}
