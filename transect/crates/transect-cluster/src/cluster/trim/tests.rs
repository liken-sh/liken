//! Tests which fields each store keeps of its objects and which fields
//! it drops.

use rstest::rstest;

use super::*;

fn object_meta() -> ObjectMeta {
    serde_yaml::from_str(
        "{annotations: {kubectl.kubernetes.io/last-applied-configuration: '{}', note: kept}, managedFields: [{manager: kubectl, operation: Apply, fieldsType: FieldsV1, fieldsV1: {'f:spec': {}}}, {manager: operator, operation: Update, subresource: status, fieldsType: FieldsV1, fieldsV1: {'f:status': {}}}]}",
    )
    .unwrap()
}

fn managers(meta: &ObjectMeta) -> Vec<(&str, Option<&str>, bool)> {
    meta.managed_fields
        .iter()
        .flatten()
        .map(|entry| {
            let manager = entry.manager.as_deref().unwrap_or_default();
            (
                manager,
                entry.subresource.as_deref(),
                entry.fields_v1.is_some(),
            )
        })
        .collect()
}

#[rstest]
#[case::everything(meta, vec![])]
#[case::all_but_status_managers(all_but_status_managers, vec![("operator", Some("status"), false)])]
fn trims_managed_fields(
    #[case] trim: fn(&mut ObjectMeta),
    #[case] expected: Vec<(&str, Option<&str>, bool)>,
) {
    let mut meta = object_meta();
    trim(&mut meta);
    assert_eq!(managers(&meta), expected);
    let annotations = meta.annotations.unwrap();
    assert_eq!(annotations.keys().collect::<Vec<_>>(), vec!["note"]);
}

const SPEC: &str = "{nodeName: alpha, serviceAccountName: web, securityContext: {runAsUser: 1000}, schedulerName: default-scheduler, containers: [{name: web, image: web:1, env: [{name: DB, value: db:5432}], securityContext: {privileged: false}, terminationMessagePath: /dev/termination-log, imagePullPolicy: Always}]}";

fn trimmed_pod() -> Pod {
    let text = format!(
        "metadata: {{name: web-1}}\nspec: {SPEC}\nstatus: {{allocatedResources: {{cpu: '1'}}, containerStatuses: [{{name: web, image: web:1, imageID: 'sha256:1', ready: true, restartCount: 2, containerID: 'containerd://1', allocatedResources: {{cpu: '1'}}}}]}}"
    );
    let mut found: Pod = serde_yaml::from_str(&text).unwrap();
    pod(&mut found);
    found
}

#[rstest]
fn keeps_what_a_pod_is_read_for() {
    let found = trimmed_pod();
    let spec = found.spec.unwrap();
    let web = &spec.containers[0];
    let env = web.env.as_ref().unwrap();
    let status = &found.status.unwrap().container_statuses.unwrap()[0];
    assert_eq!(
        (
            spec.node_name.as_deref(),
            spec.service_account_name.as_deref(),
            web.image.as_deref(),
            env[0].value.as_deref(),
            status.restart_count,
            status.image_id.as_str(),
        ),
        (
            Some("alpha"),
            Some("web"),
            Some("web:1"),
            Some("db:5432"),
            2,
            "sha256:1"
        )
    );
}

/// A running pod, trimmed by `pod`, with the YAML fragments `meta` and
/// `status` appended to its metadata and its status.
fn running(meta: &str, status: &str) -> Pod {
    let text = format!(
        "metadata: {{name: job-1{meta}}}\nspec: {SPEC}\nstatus: {{phase: Running{status}}}"
    );
    let mut found: Pod = serde_yaml::from_str(&text).unwrap();
    pod(&mut found);
    found
}

#[rstest]
#[case::a_finalizer(", finalizers: [batch.kubernetes.io/job-tracking]", "")]
#[case::a_generation(", generation: 2", "")]
#[case::a_grace_period(", deletionGracePeriodSeconds: 30", "")]
#[case::an_observed_generation("", ", observedGeneration: 2")]
#[case::its_machines_address("", ", hostIP: 10.0.0.1, hostIPs: [{ip: 10.0.0.1}]")]
fn a_pod_that_differs_only_in_unread_fields_is_the_same(#[case] meta: &str, #[case] status: &str) {
    let (stored, event) = (running("", ""), running(meta, status));
    assert!(crate::cluster::change::same(&stored, &event, &[]));
}

#[rstest]
fn drops_what_nothing_reads_from_a_pod() {
    let found = trimmed_pod();
    let spec = found.spec.unwrap();
    let web = &spec.containers[0];
    let found_status = found.status.unwrap();
    let pod_allocated = found_status.allocated_resources.is_some();
    let status = &found_status.container_statuses.unwrap()[0];
    assert_eq!(
        (
            pod_allocated,
            spec.security_context.is_some(),
            spec.scheduler_name.is_some(),
            web.security_context.is_some(),
            web.termination_message_path.is_some(),
            web.image_pull_policy.is_some(),
            status.container_id.is_some(),
            status.allocated_resources.is_some(),
        ),
        (false, false, false, false, false, false, false, false)
    );
}

#[rstest]
#[case::privileged("{privileged: true, runAsUser: 1000}", Some("{privileged: true}"))]
#[case::added_capabilities(
    "{capabilities: {add: [NET_ADMIN], drop: [ALL]}}",
    Some("{capabilities: {add: [NET_ADMIN]}}")
)]
#[case::neither("{privileged: false, capabilities: {drop: [ALL]}}", None)]
fn keeps_what_a_container_may_do_on_its_machine(
    #[case] context: &str,
    #[case] expected: Option<&str>,
) {
    let mut found: Container =
        serde_yaml::from_str(&format!("{{name: web, securityContext: {context}}}")).unwrap();
    container(&mut found);
    let expected: Option<SecurityContext> =
        expected.map(|text| serde_yaml::from_str(text).unwrap());
    assert_eq!(found.security_context, expected);
}

#[rstest]
fn trims_a_workloads_pod_template() {
    let text =
        format!("metadata: {{name: web}}\nspec: {{selector: {{}}, template: {{spec: {SPEC}}}}}");
    let mut found: Deployment = serde_yaml::from_str(&text).unwrap();
    deployment(&mut found);
    let template = found.spec.template.spec.unwrap();
    let web = &template.containers[0];
    assert_eq!(
        (template.security_context.is_some(), web.image.as_deref()),
        (false, Some("web:1"))
    );
}

fn widget(yaml: &str) -> DynamicObject {
    let text = format!("apiVersion: example.io/v1\nkind: Widget\n{yaml}");
    serde_yaml::from_str(&text).unwrap()
}

fn column(name: &str, path: &str) -> Column {
    Column {
        name: name.into(),
        kind: "string".into(),
        path: path.into(),
        priority: 0,
        steps: transect_resources::knowledge::jsonpath::parse(path),
    }
}

#[rstest]
#[case::conditions_and_generation(
    "metadata: {name: a, labels: {app: a}}\nspec: {password: hunter2}\nstatus: {observedGeneration: 3, phase: Up, conditions: [{type: Ready, status: 'True', reason: Up, message: fine, lastTransitionTime: '2026-01-01T00:00:00Z', extra: x}]}",
    vec![],
    serde_json::json!({"status": {"observedGeneration": 3, "conditions": [{"type": "Ready", "status": "True", "reason": "Up", "message": "fine", "lastTransitionTime": "2026-01-01T00:00:00Z"}]}})
)]
#[case::a_column_on_the_spec(
    "metadata: {name: a}\nspec: {size: large, password: hunter2}",
    vec![column("Size", ".spec.size")],
    serde_json::json!({KEPT: {"Size": "large"}})
)]
#[case::a_column_on_the_name(
    "metadata: {name: a}",
    vec![column("Name", ".metadata.name")],
    serde_json::json!({KEPT: {"Name": "a"}})
)]
#[case::no_status("metadata: {name: a}\nspec: {password: hunter2}", vec![], Value::Null)]
#[case::a_status_with_no_conditions("metadata: {name: a}\nstatus: {phase: Up}", vec![], Value::Null)]
fn keeps_only_the_conditions_and_columns_of_an_unread_kind(
    #[case] yaml: &str,
    #[case] columns: Vec<Column>,
    #[case] expected: Value,
) {
    let mut object = widget(yaml);
    conditions_and_columns(&mut object, &columns, &[]);
    assert_eq!(object.data, expected);
}

#[rstest]
fn keeps_the_metadata_of_an_unread_kind() {
    let mut object = widget(
        "metadata: {name: a, labels: {app: a}, annotations: {kubectl.kubernetes.io/last-applied-configuration: '{}'}}",
    );
    conditions_and_columns(&mut object, &[], &[]);
    let meta = &object.metadata;
    assert_eq!(
        (
            meta.labels.as_ref().map(|labels| labels.len()),
            meta.annotations
                .as_ref()
                .map(|annotations| annotations.len())
        ),
        (Some(1), Some(0))
    );
}

#[rstest]
fn keeps_only_a_crds_top_description_and_columns() {
    let mut crd: CustomResourceDefinition = serde_yaml::from_str(
        "metadata: {name: widgets.widgets.example.com}\n\
         spec: {group: widgets.example.com, scope: Namespaced, \
         names: {kind: Widget, plural: widgets}, versions: [{name: v1, served: true, \
         storage: true, additionalPrinterColumns: [{name: Age, type: date, \
         jsonPath: .metadata.creationTimestamp}], schema: {openAPIV3Schema: \
         {type: object, description: Turns things., properties: {spec: {type: object}}}}}]}",
    )
    .unwrap();
    definition(&mut crd);
    let version = &crd.spec.versions[0];
    let top = version.schema.as_ref().unwrap().open_api_v3_schema.as_ref();
    assert_eq!(
        (
            version.additional_printer_columns.as_ref().map(Vec::len),
            top.and_then(|top| top.properties.as_ref()).is_none(),
            top.and_then(|top| top.description.as_deref()),
        ),
        (Some(1), true, Some("Turns things."))
    );
}

#[test]
fn keeps_only_the_secrets_an_ephemeral_container_reads() {
    let mut found: Pod = serde_yaml::from_str(
        "{metadata: {name: web-1}, spec: {containers: [{name: web}], ephemeralContainers: [{name: debug, image: busybox, stdin: true, env: [{name: A, valueFrom: {secretKeyRef: {name: api-token, key: a}}}], envFrom: [{secretRef: {name: db-login}}]}]}}",
    )
    .unwrap();
    pod(&mut found);
    let expected: Vec<EphemeralContainer> = serde_yaml::from_str(
        "[{name: debug, env: [{name: A, valueFrom: {secretKeyRef: {name: api-token, key: a}}}], envFrom: [{secretRef: {name: db-login}}]}]",
    )
    .unwrap();
    assert_eq!(found.spec.unwrap().ephemeral_containers, Some(expected));
}
