use k8s_openapi::api::resource::v1::{ResourceClaim, ResourceSlice};
use rstest::rstest;

use super::*;
use crate::storage::fixtures::{claim, csi, local, nfs};

/// The machines that these tests lose.  alpha and beta accept any pod,
/// and gamma accepts only a pod that tolerates its `screen` taint.
fn nodes() -> Vec<Arc<Node>> {
    let node = |name: &str, taints: &str| {
        Arc::new(
            serde_yaml::from_str(&format!(
                "metadata: {{name: {name}, labels: {{disk: hdd, kubernetes.io/hostname: {name}}}}}\nspec: {{taints: {taints}}}"
            ))
            .unwrap(),
        )
    };
    vec![
        node("alpha", "[]"),
        node("beta", "[]"),
        node(
            "gamma",
            "[{key: dedicated, value: screen, effect: NoSchedule}]",
        ),
    ]
}

fn stranded_of(pod: &Pod, lost: &[&str]) -> Option<Stranded> {
    let (storage, devices, nodes) = (storage(), devices(), nodes());
    let lost = Lost::of(lost.iter().copied(), ["alpha", "beta", "gamma"]);
    let stranding = Stranding {
        storage: &storage,
        devices: &devices,
        nodes: &nodes,
    };
    stranding.stranded(pod, &lost)
}

/// A pod in shop on alpha that mounts `claims` and holds the claim
/// `radio` when `radio` is set.
fn pod(claims: &[&str], radio: bool) -> Pod {
    let volumes: Vec<String> = claims
        .iter()
        .map(|c| format!("{{name: {c}, persistentVolumeClaim: {{claimName: {c}}}}}"))
        .collect();
    let held = if radio {
        "resourceClaims: [{name: radio, resourceClaimName: radio}], "
    } else {
        ""
    };
    serde_yaml::from_str(&format!(
        "metadata: {{namespace: shop, name: web-0}}\nspec: {{nodeName: alpha, {held}containers: [], volumes: [{}]}}",
        volumes.join(", ")
    ))
    .unwrap()
}

fn storage() -> Storage {
    let volumes = [
        local("pv-cache", "alpha"),
        nfs("pv-media", "nas.example.com"),
        csi("pv-db", "csi.example.com", "fast"),
    ];
    let mut claims = [
        claim("shop", "cache", Some("pv-cache")),
        claim("shop", "media", Some("pv-media")),
        claim("shop", "db", Some("pv-db")),
    ];
    claims[2].spec.as_mut().unwrap().access_modes = Some(vec!["ReadWriteOnce".into()]);
    let attachment = serde_yaml::from_str(
        "metadata: {name: a}\nspec: {attacher: csi.example.com, nodeName: alpha, source: {persistentVolumeName: pv-db}}",
    )
    .unwrap();
    Storage::build(&claims, &volumes, &[]).with_attachments([&attachment])
}

/// A USB radio on alpha, allocated to the claim `radio`.
fn devices() -> Devices {
    let slice: ResourceSlice = serde_yaml::from_str(
        "metadata: {name: alpha-usb}\nspec: {driver: usb.example.com, nodeName: alpha, pool: {name: alpha, generation: 1, resourceSliceCount: 1}, devices: [{name: radio-1}]}",
    )
    .unwrap();
    let claim: ResourceClaim = serde_yaml::from_str(
        "metadata: {namespace: shop, name: radio}\nspec: {}\nstatus: {allocation: {devices: {results: [{request: r, driver: usb.example.com, pool: alpha, device: radio-1}]}}}",
    )
    .unwrap();
    Devices::build([&claim], [&slice])
}

const ALPHA: &str = "alpha";

/// A pod in shop on alpha with `spec`'s extra fields, as YAML, and
/// owned by a controller of `kind`.
fn placed(kind: &str, spec: &str) -> Pod {
    serde_yaml::from_str(&format!(
        "metadata: {{namespace: shop, name: web-0, ownerReferences: [{{apiVersion: apps/v1, kind: {kind}, name: web, uid: x, controller: true}}]}}\nspec: {{nodeName: alpha, containers: [], {spec}}}"
    ))
    .unwrap()
}

const HOSTS: &str = "affinity: {nodeAffinity: {requiredDuringSchedulingIgnoredDuringExecution: {nodeSelectorTerms: [{matchExpressions: [{key: kubernetes.io/hostname, operator: In, values: [alpha, beta]}]}]}}}";

const FOREVER: &str = "tolerations: [{operator: Exists}]";

#[rstest]
#[case::anywhere_else(placed("ReplicaSet", "hostNetwork: false"), &[ALPHA], None)]
#[case::pinned_by_its_selector(
    placed("ReplicaSet", "nodeSelector: {kubernetes.io/hostname: alpha}"),
    &[ALPHA],
    Some(Stranded::NoFit)
)]
#[case::another_machine_it_names(placed("ReplicaSet", HOSTS), &[ALPHA], None)]
#[case::every_machine_it_names_lost(placed("ReplicaSet", HOSTS), &[ALPHA, "beta"], Some(Stranded::NoFit))]
#[case::only_a_tainted_machine_left(placed("ReplicaSet", "nodeSelector: {disk: hdd}"), &[ALPHA, "beta"], Some(Stranded::NoFit))]
#[case::the_tainted_machine_tolerated(
    placed("ReplicaSet", "tolerations: [{key: dedicated, operator: Exists, tolerationSeconds: 30}]"),
    &[ALPHA, "beta"],
    None
)]
#[case::a_stateful_sets_pod(placed("StatefulSet", "hostNetwork: false"), &[ALPHA], Some(Stranded::StatefulSet { machine: ALPHA.into() }))]
#[case::never_evicted(placed("ReplicaSet", FOREVER), &[ALPHA], Some(Stranded::NeverEvicted))]
fn a_pod_is_stranded_by_where_it_may_run(
    #[case] pod: Pod,
    #[case] lost: &[&str],
    #[case] stranded: Option<Stranded>,
) {
    assert_eq!(stranded_of(&pod, lost), stranded);
}

#[rstest]
#[case::nothing_held(&["media"], false, &[ALPHA], None)]
#[case::its_machines_disk(&["media", "cache"], false, &[ALPHA], Some(Stranded::Disk { machine: ALPHA.into() }))]
#[case::an_attached_volume(&["db"], false, &[ALPHA], Some(Stranded::Attached { kind: Type::Class("fast".into()), machine: ALPHA.into() }))]
#[case::a_device(&[], true, &[ALPHA], Some(Stranded::Device { device: "usb radio-1".into(), machine: ALPHA.into() }))]
#[case::volume_before_device(&["cache"], true, &[ALPHA], Some(Stranded::Disk { machine: ALPHA.into() }))]
#[case::another_machine_lost(&["cache", "db"], true, &["beta"], None)]
fn a_pod_is_stranded_by_what_the_lost_machine_holds(
    #[case] claims: &[&str],
    #[case] radio: bool,
    #[case] lost: &[&str],
    #[case] stranded: Option<Stranded>,
) {
    assert_eq!(stranded_of(&pod(claims, radio), lost), stranded);
}
