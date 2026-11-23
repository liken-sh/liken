//! Which `Secret`s and `ConfigMap`s a pod reads, through environment
//! variables or volumes, and which of their keys. `transect` reads only
//! the pod spec, never the `Secret` or `ConfigMap` itself, so an
//! `envFrom` or a volume with no `items` is recorded as reading every
//! key. No value is stored here, so no value can reach a page.

mod sentence;

pub use sentence::NAMES_ONLY;

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::{
    ConfigMapProjection, KeyToPath, PodSpec, SecretProjection, Volume,
};

#[derive(Debug, Default, PartialEq)]
pub struct Environment {
    /// `Secret`s first, then `ConfigMap`s, each sorted by name.
    pub sources: Vec<Source>,
    /// Variables that have a literal `value` in the spec.
    pub plain: usize,
    /// Variables whose `valueFrom` reads neither a `Secret` nor a
    /// `ConfigMap`, such as a `fieldRef` or a `resourceFieldRef`.
    pub fields: usize,
    /// Every environment variable in the spec, across all containers.
    pub variables: usize,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Kind {
    Secret,
    ConfigMap,
}

#[derive(Debug, PartialEq)]
pub struct Source {
    pub kind: Kind,
    pub name: String,
    pub keys: Keys,
    /// Whether a volume mounts it as files.
    pub mounted: bool,
}

/// Which of an object's keys the pod reads.
#[derive(Debug, PartialEq)]
pub enum Keys {
    /// Only these keys.
    Named(BTreeSet<String>),
    Every,
}

impl Keys {
    fn add(&mut self, more: Keys) {
        match (self, more) {
            (Self::Every, _) => {}
            (this, Self::Every) => *this = Self::Every,
            (Self::Named(keys), Self::Named(more)) => keys.extend(more),
        }
    }
}

impl Environment {
    /// What the containers, init containers, and volumes of `spec` read.
    pub fn read(spec: &PodSpec) -> Self {
        let mut environment = Self::default();
        let containers = spec
            .containers
            .iter()
            .chain(spec.init_containers.iter().flatten());
        for container in containers {
            for variable in container.env.iter().flatten() {
                environment.variables += 1;
                let Some(from) = &variable.value_from else {
                    environment.plain += 1;
                    continue;
                };
                if let Some(secret) = &from.secret_key_ref {
                    environment.add(Kind::Secret, &secret.name, one(&secret.key), false);
                } else if let Some(config_map) = &from.config_map_key_ref {
                    environment.add(
                        Kind::ConfigMap,
                        &config_map.name,
                        one(&config_map.key),
                        false,
                    );
                } else {
                    environment.fields += 1;
                }
            }
            for from in container.env_from.iter().flatten() {
                if let Some(secret) = &from.secret_ref {
                    environment.add(Kind::Secret, &secret.name, Keys::Every, false);
                }
                if let Some(config_map) = &from.config_map_ref {
                    environment.add(Kind::ConfigMap, &config_map.name, Keys::Every, false);
                }
            }
        }
        for volume in spec.volumes.iter().flatten() {
            environment.read_volume(volume);
        }
        environment
            .sources
            .sort_by(|a, b| (a.kind, &a.name).cmp(&(b.kind, &b.name)));
        environment
    }

    fn read_volume(&mut self, volume: &Volume) {
        if let Some(secret) = &volume.secret {
            let name = secret.secret_name.as_deref().unwrap_or_default();
            self.add(Kind::Secret, name, items(secret.items.as_deref()), true);
        }
        if let Some(config_map) = &volume.config_map {
            let keys = items(config_map.items.as_deref());
            self.add(Kind::ConfigMap, &config_map.name, keys, true);
        }
        let projected = volume.projected.as_ref().and_then(|p| p.sources.as_ref());
        for projection in projected.into_iter().flatten() {
            if let Some(SecretProjection {
                name, items: list, ..
            }) = &projection.secret
            {
                self.add(Kind::Secret, name, items(list.as_deref()), true);
            }
            if let Some(ConfigMapProjection {
                name, items: list, ..
            }) = &projection.config_map
            {
                self.add(Kind::ConfigMap, name, items(list.as_deref()), true);
            }
        }
    }

    fn add(&mut self, kind: Kind, name: &str, keys: Keys, mounted: bool) {
        let found = self
            .sources
            .iter_mut()
            .find(|source| source.kind == kind && source.name == name);
        match found {
            Some(source) => {
                source.keys.add(keys);
                source.mounted |= mounted;
            }
            None => self.sources.push(Source {
                kind,
                name: name.into(),
                keys,
                mounted,
            }),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.sources.is_empty() && self.variables == 0
    }
}

fn one(key: &str) -> Keys {
    Keys::Named(BTreeSet::from([key.to_owned()]))
}

/// The keys listed in a volume's `items`, or every key when there are
/// none.
fn items(items: Option<&[KeyToPath]>) -> Keys {
    match items {
        Some(items) if !items.is_empty() => {
            Keys::Named(items.iter().map(|item| item.key.clone()).collect())
        }
        _ => Keys::Every,
    }
}

#[cfg(test)]
pub mod fixtures {
    use k8s_openapi::api::core::v1::PodSpec;

    /// A pod spec that reads one `Secret` and one `ConfigMap` by key,
    /// reads another `Secret` whole and mounts it, and mounts two
    /// `ConfigMap`s. Every plain value contains `plain-value`, so a test
    /// can check that none of them reaches the output.
    pub fn spec() -> PodSpec {
        serde_yaml_ng::from_str(
            r#"
containers:
  - name: app
    env:
      - {name: DATABASE_URL, value: postgres://plain-value-one}
      - {name: MODE, value: plain-value-two}
      - {name: DB_USER, valueFrom: {secretKeyRef: {name: database, key: username}}}
      - {name: DB_PASS, valueFrom: {secretKeyRef: {name: database, key: password}}}
      - {name: DB_PASS_AGAIN, valueFrom: {secretKeyRef: {name: database, key: password}}}
      - {name: SIGN_IN, valueFrom: {configMapKeyRef: {name: sign-in, key: issuer}}}
      - {name: NODE, valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
    envFrom:
      - secretRef: {name: api-keys}
initContainers:
  - name: migrate
    env:
      - {name: DB_HOST, valueFrom: {secretKeyRef: {name: database, key: host}}}
volumes:
  - {name: settings, configMap: {name: settings, items: [{key: app.yaml, path: app.yaml}]}}
  - {name: token, projected: {sources: [{configMap: {name: root-ca, items: [{key: ca.crt, path: ca.crt}]}}, {serviceAccountToken: {path: token}}]}}
  - {name: keys, secret: {secretName: api-keys}}
"#,
        )
        .unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::spec;
    use super::*;

    fn named(keys: &[&str]) -> Keys {
        Keys::Named(keys.iter().map(|key| key.to_string()).collect())
    }

    fn source(kind: Kind, name: &str, keys: Keys, mounted: bool) -> Source {
        Source {
            kind,
            name: name.into(),
            keys,
            mounted,
        }
    }

    #[rstest]
    fn reads_names_and_counts_from_the_spec() {
        assert_eq!(
            Environment::read(&spec()),
            Environment {
                sources: vec![
                    source(Kind::Secret, "api-keys", Keys::Every, true),
                    source(
                        Kind::Secret,
                        "database",
                        named(&["host", "password", "username"]),
                        false
                    ),
                    source(Kind::ConfigMap, "root-ca", named(&["ca.crt"]), true),
                    source(Kind::ConfigMap, "settings", named(&["app.yaml"]), true),
                    source(Kind::ConfigMap, "sign-in", named(&["issuer"]), false),
                ],
                plain: 2,
                fields: 1,
                variables: 8,
            }
        );
    }

    #[rstest]
    #[case::named_then_every(named(&["a"]), Keys::Every, Keys::Every)]
    #[case::every_then_named(Keys::Every, named(&["a"]), Keys::Every)]
    #[case::named_twice(named(&["a"]), named(&["a", "b"]), named(&["a", "b"]))]
    fn merging_keys_reads_every_key_when_either_side_does(
        #[case] first: Keys,
        #[case] then: Keys,
        #[case] expected: Keys,
    ) {
        let mut keys = first;
        keys.add(then);
        assert_eq!(keys, expected);
    }

    #[rstest]
    fn debug_output_has_no_plain_value() {
        let text = format!("{:?}", Environment::read(&spec()));
        assert!(!text.contains("plain-value"));
    }
}
