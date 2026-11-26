use rstest::rstest;

use super::super::elevation::Form;
use super::super::{KindKnowledge, parse};
use super::{Field, Format, Rows};
use crate::resources::DocId;

/// The knowledge of a kind with one elevation whose rows `rows` writes.
fn read(rows: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: towers.widgets.example.com}}\nspec:\n  elevations:\n    - name: its peals\n      question: How did each peal go?\n      rows: {rows}\n"
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
}

fn field(label: &str, path: &str, logs: bool) -> Field {
    Field {
        label: label.into(),
        path: path.into(),
        logs,
        format: Format::Text,
        failed: Vec::new(),
    }
}

#[rstest]
fn reads_the_list_the_header_and_the_columns() {
    let known = read(
        "{from: 'status.peals[]', header: [{label: phase, path: status.phase}],
          columns: [{label: ringer, path: ringer}, {label: Job, path: job, logs: true}]}",
    )
    .unwrap();
    assert_eq!(
        known.elevations[0].form,
        Form::Rows(Rows {
            from: "status.peals[]".into(),
            header: vec![field("phase", "status.phase", false)],
            columns: vec![field("ringer", "ringer", false), field("Job", "job", true)],
        })
    );
}

#[rstest]
fn reads_the_whole_object_for_its_status() {
    let known = read("{from: 'status.peals[]', columns: [{label: ringer, path: ringer}]}");
    assert!(known.unwrap().reads_fields());
}

#[rstest]
#[case::no_from(
    "{columns: [{label: ringer, path: ringer}]}",
    "elevations[0]: rows has no from"
)]
#[case::a_from_of_no_list(
    "{from: status.peals, columns: [{label: ringer, path: ringer}]}",
    "elevations[0]: rows reads status.peals, which has no []"
)]
#[case::no_columns("{from: 'status.peals[]'}", "elevations[0]: rows has no columns")]
#[case::logs_in_the_header(
    "{from: 'status.peals[]', header: [{label: Job, path: status.job, logs: true}], columns: [{label: ringer, path: ringer}]}",
    "elevations[0]: header[0] has logs"
)]
fn refuses_rows_it_cannot_draw(#[case] rows: &str, #[case] error: &str) {
    let found = read(rows).unwrap_err();
    assert!(found.contains(error), "{found}");
}

#[rstest]
#[case::a_time(Format::Time, "2026-01-01T09:00:10Z", Some("Jan 1, 09:00 UTC"))]
#[case::a_time_that_does_not_parse(Format::Time, "soon", None)]
#[case::dollars(Format::Dollars, "0.4", Some("$0.40"))]
#[case::more_dollars(Format::Dollars, "12.345", Some("$12.35"))]
#[case::text(Format::Text, "Failed", None)]
fn writes_a_value_in_its_format(
    #[case] format: Format,
    #[case] value: &str,
    #[case] expected: Option<&str>,
) {
    assert_eq!(format.write(value).as_deref(), expected);
}

#[rstest]
fn reads_a_format_and_refuses_an_unknown_one() {
    let known =
        read("{from: 'status.peals[]', columns: [{label: rung, path: rungAt, format: time}]}")
            .unwrap();
    let Form::Rows(rows) = &known.elevations[0].form else {
        panic!("not rows");
    };
    assert_eq!(rows.columns[0].format, Format::Time);
    let refused =
        read("{from: 'status.peals[]', columns: [{label: rung, path: rungAt, format: clock}]}");
    assert_eq!(
        refused.unwrap_err(),
        "elevations[0]: rung has format \"clock\", and transect reads text, time, or dollars"
    );
}
