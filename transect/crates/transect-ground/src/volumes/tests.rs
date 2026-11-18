use rstest::rstest;

use super::*;

fn yaml<K: serde::de::DeserializeOwned>(text: &str) -> Arc<K> {
    Arc::new(serde_yaml::from_str(text).unwrap())
}

/// A pod `name` in `shop` on node `alpha` in `phase`, with one container
/// that mounts the claim `data`.  `mount` is YAML appended to the
/// `volumeMounts` entry, and `source` is YAML appended to the
/// `persistentVolumeClaim` source.
fn pod(name: &str, phase: &str, source: &str, mount: &str) -> Arc<Pod> {
    yaml(&format!(
        "metadata: {{namespace: shop, name: {name}}}
spec:
  nodeName: alpha
  containers: [{{name: main, volumeMounts: [{{name: store, mountPath: /data{mount}}}]}}]
  volumes: [{{name: store, persistentVolumeClaim: {{claimName: data{source}}}}}]
status: {{phase: {phase}}}"
    ))
}

#[rstest]
#[case::a_writer(pod("web-1", "Running", "", ""), Some(true))]
#[case::a_read_only_mount(pod("web-1", "Running", "", ", readOnly: true"), Some(false))]
#[case::a_read_only_claim(pod("web-1", "Running", ", readOnly: true", ""), Some(false))]
#[case::a_finished_pod(pod("web-1", "Succeeded", "", ""), None)]
fn reads_who_mounts_a_claim_and_whether_it_writes(
    #[case] pod: Arc<Pod>,
    #[case] writes: Option<bool>,
) {
    let found = mounters(&[pod], &Claim::new("shop", "data"));
    let found: Vec<(Option<&str>, bool)> = found
        .iter()
        .map(|m| (m.machine.as_deref(), m.writes))
        .collect();
    let expected: Vec<(Option<&str>, bool)> =
        writes.into_iter().map(|w| (Some("alpha"), w)).collect();
    assert_eq!(found, expected);
}

#[rstest]
fn leaves_out_a_pod_of_another_claim() {
    let other = pod("web-1", "Running", "", "");
    assert_eq!(mounters(&[other], &Claim::new("shop", "media")), vec![]);
}

#[rstest]
fn finds_the_cron_jobs_that_mount_a_claim() {
    let job = |name: &str, claim: &str| -> Arc<CronJob> {
        yaml(&format!(
            "metadata: {{namespace: shop, name: {name}}}
spec:
  schedule: '@daily'
  jobTemplate:
    spec:
      template:
        spec:
          containers: [{{name: main}}]
          volumes: [{{name: store, persistentVolumeClaim: {{claimName: {claim}}}}}]"
        ))
    };
    let jobs = [job("backup", "data"), job("prune", "media")];
    assert_eq!(cron_jobs(&jobs, &Claim::new("shop", "data")), ["backup"]);
}

#[rstest]
#[case::bytes(512.0, "512")]
#[case::under_ten(3.2 * KI * KI * KI, "3.2Gi")]
#[case::over_ten(40.0 * KI * KI * KI, "40Gi")]
#[case::mebibytes(300.0 * KI * KI, "300Mi")]
fn writes_bytes_in_binary_units(#[case] amount: f64, #[case] text: &str) {
    assert_eq!(bytes(amount), text);
}

#[rstest]
#[case::of_a_capacity(Some("8Gi"), 2.0 * KI * KI * KI, ("2.0Gi · 25%", Some("6.0Gi")))]
#[case::with_no_capacity(None, 2.0 * KI * KI * KI, ("2.0Gi", None))]
#[case::over_its_capacity(Some("1Gi"), 2.0 * KI * KI * KI, ("2.0Gi, over its 1Gi capacity", None))]
fn reads_how_full_a_volume_is(
    #[case] capacity: Option<&str>,
    #[case] used: f64,
    #[case] expected: (&str, Option<&str>),
) {
    let (used, free) = fullness(capacity, used);
    assert_eq!((used.as_str(), free.as_deref()), expected);
}
