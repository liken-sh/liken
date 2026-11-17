use super::Link;
use crate::edges::{Edge, Endpoint, Kind, Object, Provenance};

pub fn object(kind: &str, namespace: &str, name: &str) -> Endpoint {
    Endpoint::Object(Object::new("", kind, Some(namespace), name))
}

pub fn edge(kind: Kind, from: Endpoint, to: Endpoint) -> Edge {
    Edge {
        kind,
        from,
        to,
        guessed: false,
        provenance: Provenance::Kubernetes,
    }
}

/// A forge that serves the manifests a GitOps tool applies to the forge
/// itself: the forge reconciles from its applier, which reconciles from
/// a repository in `deploy`, whose URL resolves to the forge.
pub fn forge_loop() -> Vec<Edge> {
    let forge = || object("Deployment", "forge", "forge");
    let applier = || object("Tree", "forge", "forge");
    let repository = || object("Repo", "deploy", "fleet");
    vec![
        edge(Kind::ReconcilesFrom, forge(), applier()),
        edge(Kind::ReconcilesFrom, applier(), repository()),
        edge(Kind::DependsOn, repository(), forge()),
    ]
}

pub fn link(from: &str, to: &str, kinds: &[Kind]) -> Link {
    Link {
        from: from.into(),
        to: to.into(),
        kinds: kinds.to_vec(),
    }
}
