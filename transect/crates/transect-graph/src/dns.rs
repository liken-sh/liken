//! Sorting of DNS-style names such as API groups, DRA driver names, and
//! hosts. Names sort by their labels from the right, so `liken.sh`,
//! `audio.liken.sh`, and `media.liken.sh` are together, and
//! `cert-manager.io` comes right before `acme.cert-manager.io`. Each
//! name is still shown as written. The core API group, the empty name,
//! sorts first.
//!
//! Labels compare one at a time, instead of as one reversed string,
//! because `-` sorts before `.` in a string. A reversed string would put
//! `sh.liken-x` between `sh.liken` and `sh.liken.audio`.

use std::cmp::Ordering;

/// The sort key of `name`: its lowercased labels, rightmost first.
pub fn key(name: &str) -> Vec<String> {
    name.rsplit('.').map(str::to_lowercase).collect()
}

/// The order of `a` and `b` by their labels from the right.
pub fn order(a: &str, b: &str) -> Ordering {
    a.rsplit('.')
        .map(str::to_lowercase)
        .cmp(b.rsplit('.').map(str::to_lowercase))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::core_group_first(&["apps", ""], &["", "apps"])]
    #[case::one_domain_together(
        &["media.liken.sh", "apps", "liken.sh", "batch", "audio.liken.sh"],
        &["apps", "batch", "liken.sh", "audio.liken.sh", "media.liken.sh"]
    )]
    #[case::parent_before_its_subdomains(
        &["acme.cert-manager.io", "cert-manager.io", "aquasecurity.github.io"],
        &["cert-manager.io", "acme.cert-manager.io", "aquasecurity.github.io"]
    )]
    #[case::by_label_not_by_string(
        &["audio.liken.sh", "liken-x.sh", "liken.sh"],
        &["liken.sh", "audio.liken.sh", "liken-x.sh"]
    )]
    #[case::by_top_level_domain_first(
        &["kustomize.toolkit.fluxcd.io", "secrets.example.com", "storage.k8s.io"],
        &["secrets.example.com", "kustomize.toolkit.fluxcd.io", "storage.k8s.io"]
    )]
    #[case::ignores_case(&["B.example.test", "a.example.test"], &["a.example.test", "B.example.test"])]
    fn sorts_names_by_their_labels_from_the_right(
        #[case] names: &[&str],
        #[case] expected: &[&str],
    ) {
        let mut sorted = names.to_vec();
        sorted.sort_by(|a, b| order(a, b));
        assert_eq!(sorted, expected);
        let mut keyed = names.to_vec();
        keyed.sort_by_key(|name| key(name));
        assert_eq!(keyed, expected);
    }
}
