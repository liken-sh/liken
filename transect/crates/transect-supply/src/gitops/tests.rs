//! Tests of the reason that the GitOps reader reports when it has no
//! applier to read.  The tests of the reader over the fixture cluster's
//! GitOps objects are in `web::fixture::tests::gitops`, because the
//! fixture belongs to the binary's crate.

use rstest::rstest;

use super::*;
use transect_graph::edges::Plurals;
use transect_resources::knowledge::fixtures::knowledge;

/// Knowledge of one applier kind, `trees.apply.example.test`, served
/// as `Tree`, and of a source kind that applies nothing.
fn kinds(spec: &str) -> Kinds {
    let plurals = Plurals::from([
        (("apply.example.test".into(), "Tree".into()), "trees".into()),
        (("apply.example.test".into(), "Repo".into()), "repos".into()),
    ]);
    let knowledge = [
        knowledge("trees.apply.example.test", spec),
        knowledge("repos.apply.example.test", "{repository: ['{spec.url}']}"),
    ];
    Kinds::resolve(&knowledge, &plurals, &[])
}

const APPLIES: &str = "{applies: {name: {label: tree/name}}}";

#[rstest]
fn an_off_reader_reads_nothing() {
    let off = GitOps::off(Off::NoKnowledge);
    assert_eq!(
        (off.objects().count(), off.off),
        (0, Some(Off::NoKnowledge))
    );
}

#[rstest]
#[case::no_applier("{directory: spec.path}", Watch::Listed, Some(Off::NoKnowledge))]
#[case::listed(APPLIES, Watch::Listed, None)]
#[case::listing(APPLIES, Watch::Listing, Some(Off::Listing("Trees".into())))]
#[case::refused(APPLIES, Watch::Refused, Some(Off::Refused("Trees".into())))]
fn says_why_reconciles_from_is_off(
    #[case] spec: &str,
    #[case] listing: Watch,
    #[case] expected: Option<Off>,
) {
    let found = GitOps::build(&kinds(spec), &[], |_, kind| match kind {
        "Tree" => listing,
        _ => Watch::Listed,
    });
    assert_eq!(found.off, expected);
}
