use rstest::rstest;

use super::*;

fn hint(prefix: &str) -> Hint {
    Hint::Prefix {
        prefix: prefix.into(),
        namespace: None,
    }
}

fn clue(path: &str, value: &str, hint: Hint) -> Clue {
    Clue {
        path: path.into(),
        value: value.into(),
        hint,
    }
}

fn read(yaml: &str) -> Vec<Clue> {
    clues(&serde_yaml::from_str(yaml).unwrap())
}

#[rstest]
#[case::a_secret_ref("spec: {secretRef: {name: login}}", vec![clue("spec.secretRef", "login", hint("secret"))])]
#[case::a_ref_with_its_kind(
    "spec: {sourceRef: {apiVersion: apps/v1, kind: Deployment, name: web, namespace: blog}}",
    vec![clue("spec.sourceRef", "web", Hint::Kind { group: Some("apps".into()), kind: "Deployment".into(), namespace: Some("blog".into()) })]
)]
#[case::a_service_name("spec: {serviceName: db}", vec![clue("spec.serviceName", "db", hint("service"))])]
#[case::a_name_in_a_list(
    "status: {volumes: [{claimName: data}]}",
    vec![clue("status.volumes[].claimName", "data", hint("claim"))]
)]
#[case::an_address(
    "spec: {upstream: 'db.shop.svc:5432'}",
    vec![clue("spec.upstream", "db.shop.svc:5432", Hint::Address)]
)]
#[case::a_display_name_that_is_no_object("spec: {displayName: Living Room}", vec![])]
#[case::a_plain_name("spec: {name: web, zone: hall}", vec![])]
#[case::a_sentence("status: {message: the db.shop.svc host is down}", vec![])]
#[case::an_image_is_no_address("spec: {image: registry.example.com/web:1.0}", vec![])]
#[case::a_path_is_no_address("spec: {repos: [{path: example.com/team/web}]}", vec![])]
#[case::nor_a_named_path("spec: {mountPath: /var/lib/example.d}", vec![])]
#[case::metadata_is_not_walked("metadata: {serviceName: db}", vec![])]
fn reads_each_field_named_for_what_it_names(#[case] yaml: &str, #[case] expected: Vec<Clue>) {
    assert_eq!(read(yaml), expected);
}
