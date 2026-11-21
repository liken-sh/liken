//! The readings of a part of a sketch: the printer columns of the
//! object it names, or of the object at the end of one of that object's
//! references, each as `kubectl get` prints it.

use k8s_openapi::jiff::Timestamp;
use kube::core::DynamicObject;

use super::super::super::snapshot::Snapshot;
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_map::map::Reading;
use transect_network::entry::Endpoints;
use transect_resources::knowledge::role::Column;
use transect_resources::knowledge::{ColumnRead, Followed, ReferenceEnds, columns};

/// The columns of `columns` that have a value, in their order, each
/// with the value that `kubectl get` prints: a column of `object`, or of
/// the object at the end of the column's reference, which the reading
/// links to. A reference that `Kinds::resolve` could not confirm shows
/// why in place of the value. A `Pod` and a `Service` read the columns
/// that `kubectl get` prints for them.
pub(super) fn readings(
    snapshot: &Snapshot,
    endpoints: &Endpoints,
    object: &Object,
    columns: &[Column],
    now: Timestamp,
) -> Vec<Reading> {
    if let Some(read) = super::core::readings(snapshot, endpoints, object, columns, now) {
        return read;
    }
    let reading = |column: &Column| {
        let Some(through) = &column.through else {
            let found = super::super::object::stored(snapshot, object).flatten()?;
            let version = found.api_version.clone()?;
            let value = printed(
                snapshot,
                object,
                &version,
                &found.dynamic(),
                &column.name,
                now,
            )?;
            return Some(Reading {
                name: column.name.clone(),
                value,
                from: None,
            });
        };
        if let Some(unresolved) = &through.unresolved {
            return Some(Reading {
                name: column.name.clone(),
                value: unresolved.clone(),
                from: None,
            });
        }
        let ends = snapshot.ends(object, &through.path);
        let Some(Followed::Found(other, found)) = ends.into_iter().next() else {
            return None;
        };
        let version = &found.types.as_ref()?.api_version;
        let value = printed(snapshot, &other, version, found, &column.name, now)?;
        let named = transect_graph::edges::kind_and_name(&other.kind, &other.name);
        Some(Reading {
            name: column.name.clone(),
            value,
            from: Some(Reference {
                text: format!("{} of {named}, through {}", column.name, through.path),
                href: transect_graph::links::page(&other, &snapshot.plurals),
            }),
        })
    };
    columns.iter().filter_map(reading).collect()
}

/// The value of the printer column `name` of `object`, whose stored data
/// is `found` in `api_version`, as `kubectl get` prints it, or none when
/// it is empty.
fn printed(
    snapshot: &Snapshot,
    object: &Object,
    api_version: &str,
    found: &DynamicObject,
    name: &str,
    now: Timestamp,
) -> Option<String> {
    let version = api_version.rsplit('/').next()?;
    let declared = snapshot
        .paged
        .columns
        .of(&object.group, &object.kind, version);
    let read = columns::read(declared, found, now);
    read.into_iter().find_map(|column| match column {
        ColumnRead::Value { name: read, value } if read == name && !value.is_empty() => Some(value),
        _ => None,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::super::fixture;
    use super::*;
    use transect_resources::knowledge::Through;

    #[rstest]
    #[case::in_the_order_of_reads(
        "promotions.example.test",
        "Promotion",
        "spring-sale",
        &["Ready", "Discount"],
        &[("Ready", "True"), ("Discount", "20%")]
    )]
    #[case::a_trimmed_object_without_empty_values(
        "gizmos.example.test",
        "Gizmo",
        "gizmo-red",
        &["Size", "Detail", "Colour"],
        &[("Size", "large")]
    )]
    #[case::no_such_object("gizmos.example.test", "Gizmo", "gone", &["Size"], &[])]
    fn reads_the_printer_columns_a_role_names(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] name: &str,
        #[case] reads: &[&str],
        #[case] expected: &[(&str, &str)],
    ) {
        let object = Object::new(group, kind, Some("acme-shop"), name);
        let reads: Vec<Column> = reads.iter().map(|read| column(read, None)).collect();
        let found = readings(
            &fixture::snapshot(),
            &Endpoints::default(),
            &object,
            &reads,
            Timestamp::UNIX_EPOCH,
        );
        let found: Vec<(&str, &str)> = found
            .iter()
            .map(|r| (r.name.as_str(), r.value.as_str()))
            .collect();
        assert_eq!(found, expected);
    }

    fn column(name: &str, through: Option<Through>) -> Column {
        Column {
            name: name.into(),
            through,
        }
    }

    fn unresolved(path: &str) -> Through {
        Through {
            unresolved: Some(format!("{path} does not resolve")),
            ..Through::new(path)
        }
    }

    /// A reading through a reference links to the object at its end.
    /// One that names an object the cluster does not have is left out,
    /// and one that does not resolve shows why in place of its value.
    #[rstest]
    #[case::a_stored_object(
        "Room",
        "back-room",
        Through::new("spec.thermostat"),
        Some((
            "18C".into(),
            Some((
                "Temperature of Thermostat back-thermostat, through spec.thermostat".into(),
                "/namespaces/acme-ops/thermostats.stock.example.test/back-thermostat".into()
            ))
        ))
    )]
    #[case::no_such_object("Vent", "west-vent", Through::new("spec.thermostat"), None)]
    #[case::a_reference_that_does_not_resolve(
        "Room",
        "back-room",
        unresolved("spec.thermostat"),
        Some(("spec.thermostat does not resolve".into(), None))
    )]
    fn reads_a_column_through_a_reference(
        #[case] kind: &str,
        #[case] name: &str,
        #[case] through: Through,
        #[case] expected: Option<(String, Option<(String, String)>)>,
    ) {
        let object = Object::new("stock.example.test", kind, Some("acme-ops"), name);
        let reads = [column("Temperature", Some(through))];
        let found = readings(
            &fixture::snapshot(),
            &Endpoints::default(),
            &object,
            &reads,
            Timestamp::UNIX_EPOCH,
        );
        let found = found.into_iter().next().map(|reading| {
            let from = reading
                .from
                .map(|from| (from.text, from.href.unwrap_or_default()));
            (reading.value, from)
        });
        assert_eq!(found, expected);
    }
}
