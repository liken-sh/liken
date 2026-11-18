//! The machine names in a loss that match no machine in the cluster,
//! such as a typo in `?lose=`, or a machine removed after someone copied
//! a link.  The loss leaves them out, and the page says so, because
//! otherwise the reader sees an unchanged map and thinks the loss
//! changes nothing.

/// The names in `requested` that are not in `machines`, each once, in the
/// order of `requested`.
pub fn unknown<'a>(requested: impl IntoIterator<Item = &'a str>, machines: &[&str]) -> Vec<String> {
    let mut unmatched: Vec<String> = Vec::new();
    for name in requested {
        if !machines.contains(&name) && !unmatched.iter().any(|seen| seen == name) {
            unmatched.push(name.into());
        }
    }
    unmatched
}

/// "No machine is named omega.", or for several, "No machines are
/// named omega or psi."
pub fn text(unknown: &[String]) -> Option<String> {
    match unknown {
        [] => None,
        [one] => Some(format!("No machine is named {one}.")),
        [one, two] => Some(format!("No machines are named {one} or {two}.")),
        [rest @ .., last] => Some(format!(
            "No machines are named {}, or {last}.",
            rest.join(", ")
        )),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::all_known(&["alpha"], None)]
    #[case::one_unknown(&["alpha", "omega"], Some("No machine is named omega."))]
    #[case::two_unknown(&["omega", "psi", "omega"], Some("No machines are named omega or psi."))]
    #[case::three_unknown(&["omega", "psi", "chi"], Some("No machines are named omega, psi, or chi."))]
    fn names_the_machines_the_cluster_does_not_have(
        #[case] requested: &[&str],
        #[case] expected: Option<&str>,
    ) {
        let unmatched = unknown(requested.iter().copied(), &["alpha", "beta"]);
        assert_eq!(text(&unmatched).as_deref(), expected);
    }
}
