use rstest::rstest;

use super::*;

fn read(spec: &str) -> Result<ProgramKnowledge, String> {
    let text = format!(
        "apiVersion: transect.liken.sh/v1alpha1\nkind: ProgramKnowledge\nmetadata: {{name: gofer}}\nspec: {spec}\n"
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
}

#[rstest]
fn reads_every_field() {
    let program = read(
        "{summary: Keeps a browser's logins., image: guid.foo/gofer, accepts: [{audience: gofer, verb: loads browser state, grants: {kind: BrowserProfile, group: gofer.guid.foo, path: 'spec.serviceAccounts[]'}}], injects: [{podLabel: gofer.guid.foo/profile, audience: gofer, names: {kind: BrowserProfile, group: gofer.guid.foo}}]}",
    )
    .unwrap();
    let profile = KindOf {
        group: "gofer.guid.foo".into(),
        kind: "BrowserProfile".into(),
    };
    assert_eq!(
        (
            program.name.as_str(),
            program.summary.as_deref(),
            program.images.clone()
        ),
        (
            "gofer",
            Some("Keeps a browser's logins."),
            vec!["guid.foo/gofer".to_string()]
        )
    );
    assert_eq!(
        program.accepts,
        vec![Accepts {
            audience: "gofer".into(),
            verb: "loads browser state".into(),
            grants: Some(Grants {
                kind: profile.clone(),
                path: "spec.serviceAccounts[]".into(),
            }),
        }]
    );
    assert_eq!(
        program.injects,
        vec![Injects {
            pod_label: "gofer.guid.foo/profile".into(),
            audience: "gofer".into(),
            names: Some(profile),
        }]
    );
}

#[rstest]
#[case::one_image("{image: guid.foo/web}", vec!["guid.foo/web"])]
#[case::a_list("{image: [guid.foo/web, example/web]}", vec!["guid.foo/web", "example/web"])]
fn reads_one_image_or_a_list(#[case] spec: &str, #[case] expected: Vec<&str>) {
    assert_eq!(read(spec).unwrap().images, expected);
}

#[rstest]
#[case::no_image(
    "{accepts: []}",
    "the program has no image, so transect cannot find where it runs"
)]
#[case::an_empty_list(
    "{image: []}",
    "the program has no image, so transect cannot find where it runs"
)]
#[case::a_tag(
    "{image: guid.foo/web:1.2}",
    "the image guid.foo/web:1.2 has a tag or a digest, so it would match one release only"
)]
#[case::no_audience(
    "{image: a/b, accepts: [{verb: calls}]}",
    "accepts[0]: the entry has no audience"
)]
#[case::no_verb(
    "{image: a/b, accepts: [{audience: a}]}",
    "accepts[0]: the entry has no verb, the phrase of its edge"
)]
#[case::a_grant_with_no_path(
    "{image: a/b, accepts: [{audience: a, verb: v, grants: {kind: Badge}}]}",
    "accepts[0]: grants has no path, the field that names the account"
)]
#[case::no_pod_label(
    "{image: a/b, injects: [{audience: a}]}",
    "injects[0]: the entry has no podLabel"
)]
#[case::no_injected_audience(
    "{image: a/b, injects: [{podLabel: a/b}]}",
    "injects[0]: the entry has no audience"
)]
#[case::names_with_a_path(
    "{image: a/b, injects: [{podLabel: a/b, audience: a, names: {kind: Profile, path: spec.x}}]}",
    "injects[0]: names reads the label's value, so it has no path"
)]
fn refuses_a_program_it_cannot_read(#[case] spec: &str, #[case] message: &str) {
    assert_eq!(read(spec), Err(message.into()));
}

#[rstest]
fn refuses_an_unknown_field() {
    assert!(
        read("{image: a/b, verbs: []}")
            .unwrap_err()
            .contains("unknown field `verbs`")
    );
}

#[rstest]
#[case::the_same_repository("atcr.io/guid.foo/gofer:1.4", true)]
#[case::with_a_digest("atcr.io/guid.foo/gofer@sha256:0a1b", true)]
#[case::the_entry_as_a_host_and_path("guid.foo/gofer:1", true)]
#[case::behind_a_mirror_prefix("ghcr.io/mirror/guid.foo/gofer:1", true)]
#[case::not_on_a_segment("ghcr.io/xguid.foo/gofer:1", false)]
#[case::another_program("atcr.io/guid.foo/gofer-sidecar:1", false)]
fn matches_a_container_by_its_repository(#[case] image: &str, #[case] runs: bool) {
    let program = read("{image: guid.foo/gofer}").unwrap();
    assert_eq!(program.runs(image), runs);
}
