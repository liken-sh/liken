//! Ways in, over small clusters written for them: which rows each
//! Ingress, Service, and pod gives, what each row reaches, and the
//! gates on it.

use rstest::rstest;

use super::fixture::{Cluster, gates, rows};
use super::*;

const SHOP: &str = r#"
kind: Service
metadata: {namespace: shop, name: storefront}
spec: {selector: {app: storefront}, ports: [{port: 80}]}
---
kind: Service
metadata: {namespace: shop, name: api}
spec: {selector: {app: api}, ports: [{port: 80}]}
"#;

fn shop(ingress: &str) -> Cluster {
    Cluster::of(&format!("{SHOP}---\n{ingress}"))
}

const TWO_HOSTS: &str = r#"
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  rules:
  - host: a.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
  - host: b.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: api, port: {number: 80}}}}]}
"#;

const DEFAULT_BESIDE_A_HOST: &str = r#"
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  defaultBackend: {service: {name: api, port: {number: 80}}}
  rules:
  - host: a.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
"#;

const DEFAULT_UNDER_A_PATH: &str = r#"
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  defaultBackend: {service: {name: api, port: {number: 80}}}
  rules:
  - host: a.example.test
    http: {paths: [{path: /shop, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
"#;

const UNSERVED: &str = r#"
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  ingressClassName: unserved
  rules:
  - host: a.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
"#;

#[rstest]
#[case::each_host_reaches_its_own_rules(TWO_HOSTS, &["a.example.test 80 -> storefront", "b.example.test 80 -> api"])]
#[case::a_default_backend_beside_a_host(DEFAULT_BESIDE_A_HOST, &["any host 80 -> api", "a.example.test 80 -> storefront"])]
#[case::a_default_backend_under_a_hosts_paths(DEFAULT_UNDER_A_PATH, &["any host 80 -> api", "a.example.test 80 -> storefront,api"])]
#[case::a_class_no_controller_serves(UNSERVED, &[])]
fn reads_what_each_hosts_rules_reach(#[case] ingress: &str, #[case] expected: &[&str]) {
    assert_eq!(rows(&shop(ingress).ways()), expected);
}

#[test]
fn names_an_ingress_whose_class_no_controller_serves() {
    let ways = shop(UNSERVED).ways();
    let names: Vec<&str> = ways.unserved.iter().map(|o| o.name.as_str()).collect();
    assert_eq!(names, ["shop"]);
}

const TWO_PATHS: &str = r#"
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  rules:
  - host: a.example.test
    http:
      paths:
      - {path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}
      - {path: /api, pathType: Prefix, backend: {service: {name: api, port: {number: 80}}}}
"#;

#[rstest]
#[case::every_workload_declares(TWO_PATHS, &["storefront", "api"], &[("a.example.test", State::Declared)])]
#[case::one_of_two_declares(TWO_PATHS, &["storefront"], &[("a.example.test", State::Half)])]
#[case::none_declares(TWO_PATHS, &[], &[("a.example.test", State::Off)])]
#[case::only_the_other_hosts_workload_declares(TWO_HOSTS, &["storefront"], &[("a.example.test", State::Declared), ("b.example.test", State::Off)])]
fn declares_sign_in_only_for_the_workloads_that_say_so(
    #[case] ingress: &str,
    #[case] declared: &[&str],
    #[case] expected: &[(&str, State)],
) {
    let ways = shop(ingress).declaring(declared).ways();
    let expected: Vec<(String, State)> =
        expected.iter().map(|(n, s)| (n.to_string(), *s)).collect();
    assert_eq!(gates(&ways, |w| &w.sign_in), expected);
}

fn relay(host_ip: &str) -> String {
    format!(
        r#"
kind: Pod
metadata: {{namespace: edge, name: relay-1, labels: {{app: relay}}}}
spec:
  nodeName: machine-a
  containers: [{{name: main, ports: [{{containerPort: 8080, hostPort: 8080, hostIP: '{host_ip}'}}, {{containerPort: 9090, hostPort: 9090}}]}}]
"#
    )
}

#[rstest]
#[case::every_address("0.0.0.0", &["machine-a's address 8080,9090 -> relay"])]
#[case::loopback("127.0.0.1", &["machine-a's address 9090 -> relay"])]
#[case::loopback_v6("::1", &["machine-a's address 9090 -> relay"])]
fn leaves_out_a_host_port_bound_to_loopback(#[case] host_ip: &str, #[case] expected: &[&str]) {
    assert_eq!(rows(&Cluster::of(&relay(host_ip)).ways()), expected);
}

const BALANCED: &str = r#"
kind: Service
metadata: {namespace: home, name: broker}
spec: {type: LoadBalancer, selector: {app: broker}, ports: [{port: 1883, nodePort: 30883}]}
status: {loadBalancer: {ingress: [{ip: 192.0.2.10}]}}
"#;

const BALANCED_WITHOUT_NODE_PORTS: &str = r#"
kind: Service
metadata: {namespace: home, name: broker}
spec: {type: LoadBalancer, allocateLoadBalancerNodePorts: false, selector: {app: broker}, ports: [{port: 1883}]}
status: {loadBalancer: {ingress: [{ip: 192.0.2.10}]}}
"#;

const NODE_PORT: &str = r#"
kind: Service
metadata: {namespace: home, name: broker}
spec: {type: NodePort, selector: {app: broker}, ports: [{port: 1883, nodePort: 30883}]}
"#;

const EXTERNAL_IPS: &str = r#"
kind: Service
metadata: {namespace: home, name: broker}
spec: {externalIPs: [192.0.2.20, 192.0.2.21], selector: {app: broker}, ports: [{port: 1883}]}
"#;

const CONTROLLER: &str = r#"
kind: Service
metadata: {namespace: edge, name: controller}
spec: {type: LoadBalancer, selector: {app: controller}, ports: [{port: 80, nodePort: 30080}, {port: 443, nodePort: 30443}]}
status: {loadBalancer: {ingress: [{ip: 192.0.2.30}]}}
---
kind: Service
metadata: {namespace: shop, name: storefront}
spec: {selector: {app: storefront}, ports: [{port: 80}]}
---
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  rules:
  - host: a.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
"#;

#[rstest]
#[case::a_load_balancers_node_ports(BALANCED, &["192.0.2.10 1883 -> broker", "every machine's address 30883 -> broker"])]
#[case::a_load_balancer_without_node_ports(BALANCED_WITHOUT_NODE_PORTS, &["192.0.2.10 1883 -> broker"])]
#[case::a_node_port(NODE_PORT, &["every machine's address 30883 -> broker"])]
#[case::external_ips(EXTERNAL_IPS, &["192.0.2.20, 192.0.2.21 1883 -> broker"])]
#[case::the_ingress_controllers_node_ports(CONTROLLER, &["a.example.test 80 -> storefront", "every machine's address 30080,30443 -> controller"])]
fn reads_every_address_a_service_answers_on(#[case] text: &str, #[case] expected: &[&str]) {
    assert_eq!(rows(&Cluster::of(text).ways()), expected);
}
