//! Reads the references that a `KindKnowledge` declares from one
//! object's fields: one relation for each value that each reference's
//! path reads, and one for the `Secret` that the object writes. An
//! address is only recorded here. Resolving it needs the cluster's
//! addresses and graph, so another module does that.

use serde_json::Value;

use super::referenced::render;
use super::{KindKnowledge, Matches, Reference, Target, path};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::{Kind, Object, Provenance};
use transect_graph::untrusted::capped;

/// A field value longer than this many characters is cut.
const VALUE_CHARS: usize = 200;

/// The verb a card shows for the `Secret` that an object writes.
const WRITES: &str = "writes";

/// One value of one field, and what it refers to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Relation {
    /// The verb a card shows, such as "amplified by".
    pub label: String,
    /// The field path as the knowledge writes it.
    pub path: String,
    /// The kind of edge it draws. It is none for a value that matches no
    /// single object and for the `Secret` that the object writes.
    pub edge: Option<Kind>,
    pub value: String,
    pub names: Names,
    pub provenance: Provenance,
}

/// What a relation's value refers to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Names {
    Object(Object),
    /// An address, which another module resolves against the cluster.
    Address,
    /// A value with no object: the reference declares no kind, as for a
    /// monitor, or no object matches the value.
    Value,
    /// The number of objects that match the value. No edge is drawn,
    /// because a reference refers to one object.
    Ambiguous(usize),
}

/// The relations of `object`, whose spec and status are `data`, through
/// `knowledge`: one for each value of each reference, in the order the
/// knowledge lists the references, then the `Secret` that it writes.
///
/// A reference with a `match` field refers to the object in `matches`
/// whose field equals the value. That object must be in the referring
/// object's namespace, unless the reference sets `acrossNamespaces`,
/// the referring object is cluster-scoped, or the target kind is. When
/// no object matches, the relation is `Names::Value`, and when several
/// match, it is `Names::Ambiguous`. Neither draws an edge.
///
/// A reference without `match` refers to the object named by the value,
/// in the referring object's namespace unless its kind is
/// cluster-scoped. A reference by a name template has one value, the
/// template filled from `object`, so it reads no field.
pub fn relations(
    knowledge: &KindKnowledge,
    object: &Object,
    data: &Value,
    scoped: &ClusterScoped,
    matches: &Matches,
) -> Vec<Relation> {
    let mut found = Vec::new();
    for reference in &knowledge.references {
        let label = reference.label.as_deref().unwrap_or(reference.edge.words());
        let values = match reference.by_name {
            true => vec![render(&reference.path, object)],
            false => path::read_of(object, data, &reference.path),
        };
        for value in values {
            let Some(value) = capped(&value, VALUE_CHARS) else {
                continue;
            };
            let targets: Vec<(Names, Option<Kind>)> = match &reference.target {
                Target::Object { kind, group } => {
                    let group = group.as_deref().unwrap_or_default();
                    match &reference.matched {
                        Some(field) => {
                            let matched = matches.named(group, kind, field, &value);
                            let here = object.namespace.as_deref();
                            let namespace_allows = |target: &&Object| {
                                reference.across_namespaces
                                    || here.is_none()
                                    || target.namespace.is_none()
                                    || target.namespace.as_deref() == here
                            };
                            let found: Vec<&Object> =
                                matched.iter().filter(namespace_allows).collect();
                            match found[..] {
                                [] => vec![(Names::Value, None)],
                                [one] => vec![(Names::Object(one.clone()), Some(reference.edge))],
                                _ => vec![(Names::Ambiguous(found.len()), None)],
                            }
                        }
                        None => {
                            let target = referenced_object(object, group, kind, &value, scoped);
                            vec![(Names::Object(target), Some(reference.edge))]
                        }
                    }
                }
                Target::Address => vec![(Names::Address, Some(reference.edge))],
                Target::Value => vec![(Names::Value, None)],
            };
            for (names, edge) in targets {
                found.push(Relation {
                    label: label.into(),
                    path: reference.path.clone(),
                    edge,
                    value: value.clone(),
                    names,
                    provenance: knowledge.provenance(&reference.path),
                });
            }
        }
    }
    found.extend(written_secret(knowledge, object, data));
    found
}

/// The relation for the `Secret` that `object` writes, in its own
/// namespace, named by the value at the knowledge's `writesSecret`
/// path.
pub fn written_secret(
    knowledge: &KindKnowledge,
    object: &Object,
    data: &Value,
) -> Option<Relation> {
    let field = knowledge.writes_secret.as_deref()?;
    let value = path::read_of(object, data, field).into_iter().next()?;
    let value = capped(&value, VALUE_CHARS)?;
    let secret = Object::new("", "Secret", object.namespace.as_deref(), &value);
    Some(Relation {
        label: WRITES.into(),
        path: field.into(),
        edge: None,
        value,
        names: Names::Object(secret),
        provenance: knowledge.provenance(field),
    })
}

fn referenced_object(
    from: &Object,
    group: &str,
    kind: &str,
    name: &str,
    scoped: &ClusterScoped,
) -> Object {
    let cluster_wide = scoped.contains(&(group.to_string(), kind.to_string()));
    let namespace = from.namespace.as_deref().filter(|_| !cluster_wide);
    Object::new(group, kind, namespace, name)
}

/// The group and the kind that the reference at `path` names, or none
/// when no reference is at `path` or it names no kind.
pub fn target_of<'a>(references: &'a [Reference], path: &str) -> Option<(&'a str, &'a str)> {
    let reference = references.iter().find(|r| r.path == path)?;
    match &reference.target {
        Target::Object { kind, group } => Some((group.as_deref().unwrap_or_default(), kind)),
        Target::Address | Target::Value => None,
    }
}

impl Reference {
    /// The field as a page names it for the referring `object`: its
    /// path, or the name that its template makes for the object, so a
    /// reader sees "orders-ro" and not "{name}-ro".
    pub fn field_of(&self, object: &Object) -> String {
        match self.by_name {
            true => render(&self.path, object),
            false => self.path.clone(),
        }
    }
}

impl Relation {
    /// Where the value was read, such as "from status.amplifier.name".
    pub fn source(&self) -> String {
        format!("from {}", self.path)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;
    use super::*;

    const JUKEBOX: &str = "
spec:
  dials: [{name: hall}, {name: den}]
  credentials: jukebox-login
status:
  screen: {node: alpha, monitor: tv-1}
  amplifier: {name: amp}
  bus: {address: 'broker.messages.svc:1883'}
";

    const REFERENCES: &str = "{references: [
        {path: status.screen.node, kind: Node, edge: runs on},
        {path: 'spec.dials[].name', kind: Dial, group: widgets.example.com, label: controlled by},
        {path: status.amplifier.name, kind: Amplifier, group: amps.example.com, label: amplified by},
        {path: status.screen.monitor, label: shows on},
        {path: status.bus.address, address: true, label: talks over},
        {path: 'status.horns[].name', label: heard through}],
      writesSecret: spec.credentials}";

    fn jukebox() -> Object {
        Object::new("widgets.example.com", "Jukebox", Some("shop"), "front-room")
    }

    fn scoped() -> ClusterScoped {
        ClusterScoped::from([
            ("".to_string(), "Node".to_string()),
            ("amps.example.com".to_string(), "Amplifier".to_string()),
        ])
    }

    fn read(spec: &str) -> Vec<Relation> {
        let knowledge = knowledge("jukeboxes.widgets.example.com", spec);
        let data: Value = serde_yaml::from_str(JUKEBOX).unwrap();
        relations(
            &knowledge,
            &jukebox(),
            &data,
            &scoped(),
            &Matches::default(),
        )
    }

    type Read<'a> = (&'a str, Option<Kind>, &'a str, Names);

    fn object(group: &str, kind: &str, namespace: Option<&str>, name: &str) -> Names {
        Names::Object(Object::new(group, kind, namespace, name))
    }

    #[rstest]
    fn reads_each_value_each_reference_names() {
        let found = read(REFERENCES);
        let read: Vec<Read> = found
            .iter()
            .map(|r| (r.label.as_str(), r.edge, r.value.as_str(), r.names.clone()))
            .collect();
        let dial = |name| object("widgets.example.com", "Dial", Some("shop"), name);
        assert_eq!(
            read,
            vec![
                (
                    "runs on",
                    Some(Kind::RunsOn),
                    "alpha",
                    object("", "Node", None, "alpha")
                ),
                ("controlled by", Some(Kind::DependsOn), "hall", dial("hall")),
                ("controlled by", Some(Kind::DependsOn), "den", dial("den")),
                (
                    "amplified by",
                    Some(Kind::DependsOn),
                    "amp",
                    object("amps.example.com", "Amplifier", None, "amp")
                ),
                ("shows on", None, "tv-1", Names::Value),
                (
                    "talks over",
                    Some(Kind::DependsOn),
                    "broker.messages.svc:1883",
                    Names::Address
                ),
                (
                    "writes",
                    None,
                    "jukebox-login",
                    object("", "Secret", Some("shop"), "jukebox-login")
                ),
            ]
        );
    }

    /// A `Matches` of `Amplifier` objects of `amps.example.com`. Each
    /// entry is a name, a namespace or none, and the address in the
    /// object's status.
    fn amps(amps: &[(&str, Option<&str>, &str)]) -> Matches {
        let resource = kube::core::ApiResource {
            group: "amps.example.com".into(),
            version: "v1".into(),
            api_version: "amps.example.com/v1".into(),
            kind: "Amplifier".into(),
            plural: "amplifiers".into(),
        };
        let objects = amps.iter().map(|(name, namespace, address)| {
            let namespace = namespace.map_or(String::new(), |n| format!(", namespace: {n}"));
            let text =
                format!("metadata: {{name: {name}{namespace}}}\nstatus: {{address: '{address}'}}");
            std::sync::Arc::new(serde_yaml::from_str(&text).unwrap())
        });
        let key = (
            "amps.example.com".to_string(),
            "Amplifier".to_string(),
            "status.address".to_string(),
        );
        Matches::build([key], &[(resource, objects.collect())])
    }

    const BY_ADDRESS: &str = "{references: [{path: status.bus.address, kind: Amplifier, group: amps.example.com, match: status.address, label: amplified by}]}";

    const ACROSS: &str = "{references: [{path: status.bus.address, kind: Amplifier, group: amps.example.com, match: status.address, acrossNamespaces: true}]}";

    const BUS: &str = "broker.messages.svc:1883";

    fn amp(namespace: Option<&str>, name: &str) -> Names {
        object("amps.example.com", "Amplifier", namespace, name)
    }

    #[rstest]
    #[case::one_match(
        BY_ADDRESS,
        &[("amp", None, BUS), ("spare", None, "elsewhere:1883")],
        vec![(Some(Kind::DependsOn), amp(None, "amp"))]
    )]
    #[case::no_match(BY_ADDRESS, &[("spare", None, "elsewhere:1883")], vec![(None, Names::Value)])]
    #[case::two_matches_draw_no_edge(
        BY_ADDRESS,
        &[("amp", Some("shop"), BUS), ("other", Some("shop"), BUS)],
        vec![(None, Names::Ambiguous(2))]
    )]
    #[case::one_in_its_own_namespace_of_two(
        BY_ADDRESS,
        &[("amp", Some("shop"), BUS), ("other", Some("blog"), BUS)],
        vec![(Some(Kind::DependsOn), amp(Some("shop"), "amp"))]
    )]
    #[case::only_in_another_namespace(
        BY_ADDRESS,
        &[("other", Some("blog"), BUS)],
        vec![(None, Names::Value)]
    )]
    #[case::across_namespaces_when_the_reference_says(
        ACROSS,
        &[("other", Some("blog"), BUS)],
        vec![(Some(Kind::DependsOn), amp(Some("blog"), "other"))]
    )]
    fn refers_to_the_one_object_whose_field_matches_the_value(
        #[case] spec: &str,
        #[case] amps_held: &[(&str, Option<&str>, &str)],
        #[case] expected: Vec<(Option<Kind>, Names)>,
    ) {
        let knowledge = knowledge("jukeboxes.widgets.example.com", spec);
        let data: Value = serde_yaml::from_str(JUKEBOX).unwrap();
        let found = relations(&knowledge, &jukebox(), &data, &scoped(), &amps(amps_held));
        let read: Vec<(Option<Kind>, Names)> =
            found.into_iter().map(|r| (r.edge, r.names)).collect();
        assert_eq!(read, expected);
    }

    #[rstest]
    fn marks_each_relation_with_its_field() {
        let found = read("{references: [{path: status.amplifier.name, kind: Amplifier}]}");
        let provenance = Provenance::Kinds {
            knowledge: "jukeboxes.widgets.example.com".into(),
            field: "status.amplifier.name".into(),
        };
        assert_eq!(
            found
                .iter()
                .map(|r| (r.provenance.clone(), r.source()))
                .collect::<Vec<_>>(),
            vec![(provenance, "from status.amplifier.name".to_string())]
        );
    }

    #[rstest]
    #[case::in_its_namespace(
        "{references: [{name: '{name}-ro', kind: Service, label: reads through}]}",
        vec![("{name}-ro", "front-room-ro", object("", "Service", Some("shop"), "front-room-ro"))]
    )]
    #[case::with_the_namespace(
        "{references: [{name: '{namespace}-{name}', kind: Amplifier, group: amps.example.com}]}",
        vec![("{namespace}-{name}", "shop-front-room", object("amps.example.com", "Amplifier", None, "shop-front-room"))]
    )]
    fn reads_the_name_that_a_template_makes(
        #[case] spec: &str,
        #[case] expected: Vec<(&str, &str, Names)>,
    ) {
        let found = read(spec);
        let read: Vec<(&str, &str, Names)> = found
            .iter()
            .map(|r| (r.path.as_str(), r.value.as_str(), r.names.clone()))
            .collect();
        assert_eq!(read, expected);
    }

    #[rstest]
    #[case::a_field("{writesSecret: spec.credentials}", "jukebox-login")]
    #[case::its_own_name("{writesSecret: metadata.name}", "front-room")]
    fn reads_the_secret_it_writes(#[case] spec: &str, #[case] secret: &str) {
        let knowledge = knowledge("jukeboxes.widgets.example.com", spec);
        let data: Value = serde_yaml::from_str(JUKEBOX).unwrap();
        let found = written_secret(&knowledge, &jukebox(), &data).map(|r| r.names);
        assert_eq!(found, Some(object("", "Secret", Some("shop"), secret)));
    }

    #[rstest]
    #[case::a_missing_field("{references: [{path: status.display.name, kind: Display}]}")]
    #[case::no_references("{}")]
    #[case::a_missing_secret_field("{writesSecret: spec.password}")]
    fn reads_nothing_when_the_field_is_missing(#[case] spec: &str) {
        assert_eq!(read(spec), vec![]);
    }
}
