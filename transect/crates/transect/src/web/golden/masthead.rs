//! The masthead links the vulnerabilities only when some kind knowledge
//! loaded describes a scanner's reports.

use rstest::rstest;

use super::super::fixture;
use super::render_from;

#[rstest]
#[case::described("  findings:", true)]
#[case::not_described("  findings:", false)]
fn links_the_vulnerabilities_when_knowledge_describes_reports(
    #[case] key: &str,
    #[case] described: bool,
) {
    let (model, usage) = fixture::edited(|text| match described {
        true => text.to_string(),
        false => text.replace(key, "  findngs:"),
    });
    let html = render_from(&model, &usage, "/");
    assert_eq!(html.contains("href=\"/vulnerabilities\""), described);
}
