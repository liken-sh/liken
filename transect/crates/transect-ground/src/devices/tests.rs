use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::resource::v1::{ResourceClaim, ResourceSlice};
use rstest::rstest;

use super::*;
use transect_cluster::ownership::fixtures::meta;
use transect_cluster::ownership::{Entry, Ownership};
use transect_graph::edges::fixtures::device;
use transect_graph::edges::{Device, Endpoint, Graph, Object, Provenance};

/// A slice of `driver`'s pool on `node` at `generation`, listing
/// `devices` as YAML.
fn slice(driver: &str, node: Option<&str>, generation: i64, devices: &str) -> ResourceSlice {
    let node = node
        .map(|node| format!("nodeName: {node}, "))
        .unwrap_or("allNodes: true, ".into());
    let pool = "alpha";
    serde_yaml::from_str(&format!(
        "metadata: {{name: {pool}-{driver}}}\nspec: {{driver: {driver}, {node}pool: {{name: {pool}, generation: {generation}, resourceSliceCount: 1}}, devices: {devices}}}"
    ))
    .unwrap()
}

/// A claim in shop whose allocation names the `devices` of the alpha
/// pool, each by driver and name.
fn claim(name: &str, devices: &[(&str, &str)]) -> ResourceClaim {
    pinned(name, devices, "{nodeSelectorTerms: []}")
}

/// `claim`, with its allocation pinned to the nodes `selector` admits.
fn pinned(name: &str, devices: &[(&str, &str)], selector: &str) -> ResourceClaim {
    let results: Vec<String> = devices
        .iter()
        .map(|(driver, device)| {
            format!("{{request: r, driver: {driver}, pool: alpha, device: {device}}}")
        })
        .collect();
    serde_yaml::from_str(&format!(
        "metadata: {{namespace: shop, name: {name}}}\nspec: {{}}\nstatus: {{allocation: {{nodeSelector: {selector}, devices: {{results: [{}]}}}}}}",
        results.join(", ")
    ))
    .unwrap()
}

/// A pod in shop, owned by the DaemonSet `radio`, with `spec` and
/// `status` as YAML.
fn pod(name: &str, spec: &str, status: &str) -> Pod {
    let mut pod: Pod = serde_yaml::from_str(&format!("spec: {spec}\nstatus: {status}")).unwrap();
    pod.metadata = meta(Some("shop"), name, &[("apps/v1", "DaemonSet", "radio")]);
    pod
}

/// A pod whose claim was made from a template, so only its status names it.
fn made(name: &str, claim: &str, phase: &str) -> Pod {
    pod(
        name,
        "{nodeName: alpha, containers: [], resourceClaims: [{name: radio, resourceClaimTemplateName: radio}]}",
        &format!(
            "{{phase: {phase}, resourceClaimStatuses: [{{name: radio, resourceClaimName: {claim}}}]}}"
        ),
    )
}

#[rstest]
#[case::on_its_slices_machine(
    vec![slice("radio.example.com", Some("alpha"), 1, "[{name: usb-1}]")],
    Some("alpha")
)]
#[case::on_the_machine_the_device_names(
    vec![slice("radio.example.com", None, 1, "[{name: usb-1, nodeName: beta}]")],
    Some("beta")
)]
#[case::on_no_one_machine(vec![slice("radio.example.com", None, 1, "[{name: usb-1}]")], None)]
#[case::the_newest_generation_wins(
    vec![
        slice("radio.example.com", Some("beta"), 2, "[{name: usb-1}]"),
        slice("radio.example.com", Some("alpha"), 1, "[{name: usb-1}]"),
    ],
    Some("beta")
)]
#[case::no_slice_lists_it(
    vec![slice("radio.example.com", Some("alpha"), 1, "[{name: usb-2}]")],
    Some("gamma")
)]
#[case::another_drivers_slice(
    vec![slice("gpu.example.com", Some("alpha"), 1, "[{name: usb-1}]")],
    Some("gamma")
)]
fn places_a_device_on_its_slices_machine_before_its_allocations(
    #[case] slices: Vec<ResourceSlice>,
    #[case] machine: Option<&str>,
) {
    let claims = [pinned(
        "radio-x",
        &[("radio.example.com", "usb-1")],
        "{nodeSelectorTerms: [{matchFields: [{key: metadata.name, operator: In, values: [gamma]}]}]}",
    )];
    let devices = Devices::build(&claims, &slices);
    let held = devices.of_pod(&made("radio-1", "radio-x", "Running"));
    let machines: Vec<Option<&str>> = held.iter().map(|d| d.machine.as_deref()).collect();
    assert_eq!(machines, vec![machine]);
}

#[rstest]
#[case::one_node(
    "{nodeSelectorTerms: [{matchFields: [{key: metadata.name, operator: In, values: [gamma]}]}]}",
    Some("gamma")
)]
#[case::two_nodes(
    "{nodeSelectorTerms: [{matchFields: [{key: metadata.name, operator: In, values: [gamma, delta]}]}]}",
    None
)]
#[case::by_label(
    "{nodeSelectorTerms: [{matchExpressions: [{key: zone, operator: In, values: [a]}]}]}",
    None
)]
#[case::two_terms(
    "{nodeSelectorTerms: [{matchFields: [{key: metadata.name, operator: In, values: [gamma]}]}, {matchFields: [{key: metadata.name, operator: In, values: [delta]}]}]}",
    None
)]
#[case::no_terms("{nodeSelectorTerms: []}", None)]
fn places_a_device_no_slice_lists_on_the_machine_its_allocation_pins(
    #[case] selector: &str,
    #[case] machine: Option<&str>,
) {
    let claims = [pinned(
        "radio-x",
        &[("radio.example.com", "usb-1")],
        selector,
    )];
    let devices = Devices::build(&claims, &[]);
    let held = devices.of_pod(&made("radio-1", "radio-x", "Running"));
    let machines: Vec<Option<&str>> = held.iter().map(|d| d.machine.as_deref()).collect();
    assert_eq!(machines, vec![machine]);
}

#[rstest]
#[case::made_from_a_template(made("radio-1", "radio-x", "Running"), vec!["usb-1"])]
#[case::named_in_the_spec(
    pod("radio-1", "{containers: [], resourceClaims: [{name: radio, resourceClaimName: radio-x}]}", "{}"),
    vec!["usb-1"]
)]
#[case::two_claims_one_device_each(
    pod(
        "radio-1",
        "{containers: [], resourceClaims: [{name: radio, resourceClaimName: radio-x}, {name: gpu, resourceClaimName: gpu-x}]}",
        "{}"
    ),
    vec!["usb-1", "card-0", "card-1"]
)]
#[case::an_unallocated_claim(made("radio-1", "waiting", "Pending"), vec![])]
#[case::no_claims(pod("radio-1", "{containers: []}", "{}"), vec![])]
fn finds_what_a_pod_holds(#[case] pod: Pod, #[case] names: Vec<&str>) {
    let claims = [
        claim("radio-x", &[("radio.example.com", "usb-1")]),
        claim(
            "gpu-x",
            &[("gpu.example.com", "card-0"), ("gpu.example.com", "card-1")],
        ),
        claim("waiting", &[]),
    ];
    let devices = Devices::build(&claims, &[]);
    let held: Vec<String> = devices.of_pod(&pod).into_iter().map(|d| d.name).collect();
    assert_eq!(held, names);
}

fn ownership(pods: &[Pod]) -> Ownership {
    Ownership::build(pods.iter().map(|pod| Entry {
        group: "",
        kind: "Pod",
        meta: &pod.metadata,
    }))
}

#[rstest]
#[case::one_pod(vec![made("radio-1", "radio-x", "Running")], vec!["usb-1"])]
#[case::replicas_sharing_a_claim(
    vec![made("radio-1", "radio-x", "Running"), made("radio-2", "radio-x", "Running")],
    vec!["usb-1"]
)]
#[case::replicas_with_a_claim_each(
    vec![made("radio-1", "radio-x", "Running"), made("radio-2", "radio-y", "Running")],
    vec!["usb-1", "usb-2"]
)]
#[case::a_pod_that_finished(vec![made("radio-1", "radio-x", "Succeeded")], vec![])]
fn a_workload_holds_its_pods_devices(#[case] pods: Vec<Pod>, #[case] names: Vec<&str>) {
    let claims = [
        claim("radio-x", &[("radio.example.com", "usb-1")]),
        claim("radio-y", &[("radio.example.com", "usb-2")]),
    ];
    let slices = [slice(
        "radio.example.com",
        Some("alpha"),
        1,
        "[{name: usb-1}, {name: usb-2}]",
    )];
    let devices = Devices::build(&claims, &slices);
    let radio = Endpoint::Object(Object::new("apps", "DaemonSet", Some("shop"), "radio"));
    let edges = holds(&devices, &pods, &ownership(&pods));
    let expected: Vec<(Endpoint, Endpoint)> = names
        .iter()
        .map(|name| {
            let to = Endpoint::Device(device("radio.example.com", "alpha", name));
            (radio.clone(), to)
        })
        .collect();
    let found: Vec<(Endpoint, Endpoint)> = edges.into_iter().map(|e| (e.from, e.to)).collect();
    assert_eq!(found, expected);
}

fn machine(name: &str) -> crate::machines::Machine {
    let nodes: Vec<k8s_openapi::api::core::v1::Node> =
        vec![serde_yaml::from_str(&format!("metadata: {{name: {name}}}")).unwrap()];
    crate::machines::machines(&nodes, &[], None).remove(0)
}

fn held_by(namespace: &str, device: Device) -> transect_graph::edges::Edge {
    transect_graph::edges::Edge {
        kind: Kind::Holds,
        from: Endpoint::Object(Object::new("apps", "DaemonSet", Some(namespace), "radio")),
        to: Endpoint::Device(device),
        guessed: false,
        provenance: Provenance::Kubernetes,
    }
}

#[rstest]
fn a_strip_names_the_devices_the_selected_namespace_holds_there() {
    let graph = Graph::new([vec![
        held_by("shop", device("radio.example.com", "alpha", "usb-1")),
        held_by("shop", device("gpu.example.com", "alpha", "card-0")),
        held_by("shop", device("gpu.example.com", "beta", "card-0")),
        held_by("blog", device("gpu.example.com", "gamma", "card-0")),
    ]]);
    let mut machines = [machine("alpha"), machine("beta"), machine("gamma")];
    name_here(&mut machines, &graph, "shop");
    let text: Vec<Option<String>> = machines.iter().map(|m| m.held_text()).collect();
    assert_eq!(
        text,
        vec![
            Some("holds gpu card-0, radio usb-1".into()),
            Some("holds gpu card-0".into()),
            None,
        ]
    );
}

#[rstest]
fn lists_what_each_machine_publishes_and_what_is_held() {
    let slices = [slice(
        "radio.example.com",
        Some("alpha"),
        1,
        "[{name: usb-2}, {name: usb-1}]",
    )];
    let claims = [
        claim("tuner", &[("radio.example.com", "usb-2")]),
        claim("scanner", &[("radio.example.com", "usb-2")]),
    ];
    let devices = Devices::build(&claims, &slices);
    let published: Vec<(&str, Vec<Object>)> = devices
        .published_on("alpha")
        .into_iter()
        .map(|published| (published.device.name.as_str(), published.claims))
        .collect();
    let claim = |name: &str| Object::new("resource.k8s.io", "ResourceClaim", Some("shop"), name);
    assert_eq!(
        (published, devices.published_on("beta").len()),
        (
            vec![
                ("usb-1", vec![]),
                ("usb-2", vec![claim("scanner"), claim("tuner")])
            ],
            0
        )
    );
}
