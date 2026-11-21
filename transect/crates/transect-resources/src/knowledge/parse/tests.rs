use rstest::rstest;

use super::*;

fn read(name: &str, spec: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "apiVersion: transect.liken.sh/v1alpha1\nkind: KindKnowledge\nmetadata: {{name: {name}}}\nspec: {spec}\n"
    );
    parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
}

#[rstest]
#[case::none_given("{healthy: {condition: Ready}}", vec![])]
#[case::two("{healthy: {condition: Ready, calm: [Idle, Inventory]}}", vec!["Idle", "Inventory"])]
fn reads_the_calm_reasons(#[case] spec: &str, #[case] expected: Vec<&str>) {
    let knowledge = read("caches.cache.example.io", spec).unwrap();
    assert_eq!(knowledge.calm(), expected);
}

#[rstest]
fn reads_a_health_through_a_reference() {
    let knowledge = read(
        "caches.cache.example.io",
        "{references: [{path: status.disk, kind: Disk, group: disks.example.io}], healthy: {through: status.disk}}",
    )
    .unwrap();
    assert_eq!(
        (knowledge.condition(), knowledge.healthy.clone()),
        (None, Some(Healthy::Through(Through::new("status.disk"))))
    );
}

#[rstest]
fn reads_every_field() {
    let knowledge = read(
        "caches.cache.example.io",
        "{summary: Keeps sessions., layer: platform, healthy: {condition: Ready}, references: [{path: status.primary.node, kind: Node, edge: runs on}, {path: spec.upstream, address: true, label: fills from}, {path: status.display}], writesSecret: spec.credentialsSecret, controller: {image: example/cache-operator}, disruptions: operator, primary: status.currentPrimary, docs: 'https://cache.example.io/docs/'}",
    )
    .unwrap();
    assert_eq!(
        (
            knowledge.plural.as_str(),
            knowledge.group.as_str(),
            knowledge.summary.as_deref(),
            knowledge.layer.as_deref(),
            knowledge.condition(),
            knowledge.writes_secret.as_deref(),
            knowledge.controller_image.as_deref(),
            knowledge.disruptions,
            knowledge.primary.as_deref(),
        ),
        (
            "caches",
            "cache.example.io",
            Some("Keeps sessions."),
            Some("platform"),
            Some("Ready"),
            Some("spec.credentialsSecret"),
            Some("example/cache-operator"),
            Some(Disruptions::Operator),
            Some("status.currentPrimary"),
        )
    );
    let references: Vec<(&str, &Target, Kind, Option<&str>)> = knowledge
        .references
        .iter()
        .map(|r| (r.path.as_str(), &r.target, r.edge, r.label.as_deref()))
        .collect();
    let node = Target::Object {
        kind: "Node".into(),
        group: None,
    };
    assert_eq!(
        references,
        vec![
            ("status.primary.node", &node, Kind::RunsOn, None),
            (
                "spec.upstream",
                &Target::Address,
                Kind::DependsOn,
                Some("fills from")
            ),
            ("status.display", &Target::Value, Kind::DependsOn, None),
        ]
    );
}

#[rstest]
#[case::by_its_name("{node: metadata.name}", Some("metadata.name"))]
#[case::unsaid("{}", None)]
fn reads_the_field_that_names_a_node(#[case] spec: &str, #[case] node: Option<&str>) {
    let knowledge = read("machines.metal.example.io", spec).unwrap();
    assert_eq!(knowledge.node.as_deref(), node);
}

#[rstest]
#[case::reconciles_from("reconciles from", Kind::ReconcilesFrom)]
#[case::part_of("part of", Kind::PartOf)]
fn reads_an_edge_from_the_vocabulary(#[case] edge: &str, #[case] expected: Kind) {
    let spec = format!("{{references: [{{path: spec.repository, kind: Repo, edge: {edge}}}]}}");
    let knowledge = read("caches.cache.example.io", &spec).unwrap();
    assert_eq!(knowledge.references[0].edge, expected);
}

#[rstest]
fn reads_the_annotations_that_name_the_kind() {
    let knowledge = read(
        "caches.cache.example.io",
        "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: example.com/caches, name: '{namespace}-{name}@cache'}]}",
    )
    .unwrap();
    assert_eq!(
        knowledge.referenced_by,
        vec![ReferencedBy {
            group: "networking.k8s.io".into(),
            kind: "Ingress".into(),
            annotation: "example.com/caches".into(),
            name: "{namespace}-{name}@cache".into(),
            role: None,
        }]
    );
}

#[rstest]
fn reads_what_a_referenced_object_does() {
    let knowledge = read(
        "certificates.certs.example.io",
        "{writesSecret: spec.secretName, references: [{path: spec.issuerRef.name, kind: ClusterIssuer, group: certs.example.io, role: issues-tls}]}",
    )
    .unwrap();
    assert_eq!(knowledge.references[0].role, Some(Duty::IssuesTls));
}

#[rstest]
fn reads_what_a_named_object_does() {
    let knowledge = read(
        "issuers.certs.example.io",
        "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: certs.example.io/issuer, name: '{name}', role: issues-tls}]}",
    )
    .unwrap();
    assert_eq!(knowledge.referenced_by[0].role, Some(Duty::IssuesTls));
}

#[rstest]
#[case::a_plural_alone("caches")]
#[case::a_core_kind("pods.v1")]
#[case::a_kind_not_a_plural("Cache.cache.example.io")]
#[case::a_trailing_dot("caches.cache.example.")]
fn refuses_a_name_that_is_not_plural_dot_group(#[case] name: &str) {
    let error = read(name, "{}").unwrap_err();
    assert!(error.contains("<plural>.<group>"), "{error}");
}

#[rstest]
#[case::an_unknown_edge(
    "{references: [{path: spec.a, edge: calls}]}",
    "references[0]: edge \"calls\" is not one of"
)]
#[case::a_kind_and_an_address(
    "{references: [{path: spec.a, kind: Service, address: true}]}",
    "references[0]: kind and address are both set"
)]
#[case::a_match_with_no_kind(
    "{references: [{path: status.radio, match: status.address}]}",
    "references[0]: match reads a field of the referenced object"
)]
#[case::a_role_with_no_kind(
    "{references: [{path: spec.issuer, role: issues-tls}]}",
    "references[0]: role applies to a referenced object"
)]
#[case::an_unknown_field("{healthy: {condition: Ready}, colour: blue}", "unknown field")]
#[case::a_health_through_no_reference(
    "{healthy: {through: status.disk}}",
    "healthy follows status.disk, which is the path of no entry of references that names a kind"
)]
#[case::a_health_through_a_value(
    "{references: [{path: status.disk}], healthy: {through: status.disk}}",
    "healthy follows status.disk, which is the path of no entry of references that names a kind"
)]
#[case::a_health_through_with_calm(
    "{references: [{path: status.disk, kind: Disk}], healthy: {through: status.disk, calm: [Idle]}}",
    "healthy follows status.disk and has calm"
)]
#[case::a_health_of_both_kinds(
    "{references: [{path: status.disk, kind: Disk}], healthy: {condition: Ready, through: status.disk}}",
    "healthy has one of condition and through"
)]
#[case::other_disruptions("{disruptions: never}", "transect reads only \"operator\"")]
#[case::a_name_with_no_name(
    "{referencedBy: [{kind: Ingress, annotation: example.com/caches, name: '{namespace}'}]}",
    "referencedBy[0]: name \"{namespace}\" has no {name}"
)]
#[case::an_unknown_role(
    "{referencedBy: [{kind: Ingress, annotation: example.com/caches, name: '{name}', role: guards}]}",
    "referencedBy[0]: role is \"guards\", and transect reads only"
)]
fn refuses_what_it_cannot_read(#[case] spec: &str, #[case] error: &str) {
    let found = read("caches.cache.example.io", spec).unwrap_err();
    assert!(found.contains(error), "{found}");
}
