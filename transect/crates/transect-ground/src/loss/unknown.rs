//! The machine names in a loss that match no machine in the cluster,
//! such as a typo in `?lose=`, or a machine removed after someone copied
//! a link.  The loss leaves them out, and the page shows that it did,
//! so the reader does not take an unchanged map for the answer.

/// The names in `asked` that are not in `machines`, each once, in the
/// order of `asked`.
pub fn unknown<'a>(asked: impl IntoIterator<Item = &'a str>, machines: &[&str]) -> Vec<String> {
    let mut found: Vec<String> = Vec::new();
    for name in asked {
        if !machines.contains(&name) && !found.iter().any(|f| f == name) {
            found.push(name.into());
        }
    }
    found
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
        #[case] asked: &[&str],
        #[case] expected: Option<&str>,
    ) {
        let found = unknown(asked.iter().copied(), &["alpha", "beta"]);
        assert_eq!(text(&found).as_deref(), expected);
    }
}
