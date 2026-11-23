use kube::core::DynamicObject;
use rstest::rstest;

use super::super::{Names, Relation};
use super::*;
use transect_graph::edges::Provenance;

fn pad(name: &str) -> Object {
    Object::new("pads.example.com", "Pad", None, name)
}

fn relation(path: &str, names: Names) -> Relation {
    Relation {
        label: "pairs as".into(),
        path: path.into(),
        edge: None,
        value: "a".into(),
        names,
        provenance: Provenance::Kubernetes,
    }
}

fn stored() -> DynamicObject {
    serde_yaml_ng::from_str("{apiVersion: pads.example.com/v1, kind: Pad, metadata: {name: kept}}")
        .unwrap()
}

/// What `follow` finds at `status.pad` among `relations`, where the
/// store of pads holds only `kept`, and no store holds a dial.
fn followed(relations: &[Relation]) -> String {
    let object = stored();
    let store = |wanted: &Object| match wanted.kind.as_str() {
        "Pad" => Some((wanted.name == "kept").then_some(&object)),
        _ => None,
    };
    let ends = follow(relations, "status.pad", store).into_iter();
    let ends = ends.map(|end| match end {
        Followed::Found(found, _) => format!("found {}", found.name),
        Followed::Ambiguous(count) => format!("{count} matches"),
        Followed::Missing(missing) => format!("missing {}", missing.name),
        Followed::Unwatched(unwatched) => format!("unwatched {}", unwatched.name),
    });
    ends.collect::<Vec<_>>().join(", ")
}

#[rstest]
#[case::a_stored_object(vec![relation("status.pad", Names::Object(pad("kept")))], "found kept")]
#[case::each_of_several(
    vec![
        relation("status.pad", Names::Object(pad("kept"))),
        relation("status.pad", Names::Object(pad("spare"))),
    ],
    "found kept, missing spare"
)]
#[case::a_value_that_matches_several(vec![relation("status.pad", Names::Ambiguous(2))], "2 matches")]
#[case::an_object_the_store_lacks(vec![relation("status.pad", Names::Object(pad("gone")))], "missing gone")]
#[case::a_kind_no_store_holds(
    vec![relation("status.pad", Names::Object(Object::new("pads.example.com", "Dial", None, "d")))],
    "unwatched d"
)]
#[case::no_value(vec![relation("status.other", Names::Object(pad("kept")))], "")]
#[case::a_value_that_names_no_object(vec![relation("status.pad", Names::Value)], "")]
fn follows_a_reference_to_each_object_it_names(
    #[case] relations: Vec<Relation>,
    #[case] expected: &str,
) {
    assert_eq!(followed(&relations), expected);
}
