use rstest::rstest;

use super::*;

fn wire(via: Option<&str>) -> Wire {
    Wire {
        from: "hopper".into(),
        to: "bin".into(),
        via: via.map(String::from),
        carries: "grain".into(),
    }
}

#[rstest]
#[case::two_ends(None, vec![("hopper", "bin")])]
#[case::a_role_between(
    Some("sifter"),
    vec![("hopper", "sifter"), ("sifter", "bin"), ("hopper", "bin")]
)]
fn names_each_pair_of_roles_the_page_can_join(
    #[case] via: Option<&str>,
    #[case] expected: Vec<(&str, &str)>,
) {
    assert_eq!(wire(via).pairs(), expected);
}
