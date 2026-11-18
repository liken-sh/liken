//! `src/frame/brand/liken.css` copies only the color tokens of
//! `brand/liken.css` at the top of the repository. The rest of that
//! file styles the project's sites and would change the layout of
//! `transect`. This test fails when a token of the copy is missing from
//! the same color scheme of the original, or has a different value.

use std::path::Path;

/// The media query that starts the dark scheme in both files.
const DARK: &str = "@media (prefers-color-scheme: dark)";

/// The `--name: value;` declarations of the light or the dark scheme of
/// a stylesheet, in file order.
fn tokens(css: &str, dark: bool) -> Vec<(String, String)> {
    let start = css.find(DARK).expect("the stylesheet has a dark scheme");
    let scheme = if dark {
        let rest = &css[start..];
        &rest[..rest.find("\n}\n").unwrap_or(rest.len())]
    } else {
        &css[..start]
    };
    scheme
        .lines()
        .filter_map(|line| line.trim().strip_prefix("--"))
        .filter_map(|line| line.strip_suffix(';')?.split_once(':'))
        .map(|(name, value)| (name.trim().to_string(), value.trim().to_string()))
        .collect()
}

fn read(path: &Path) -> String {
    std::fs::read_to_string(path)
        .unwrap_or_else(|error| panic!("reading {}: {error}", path.display()))
}

#[rstest::rstest]
#[case::light(false)]
#[case::dark(true)]
fn each_copied_token_equals_the_brand_token(#[case] dark: bool) {
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let copy = read(&crate_dir.join("src/frame/brand/liken.css"));
    let brand = read(&crate_dir.join("../../../brand/liken.css"));
    let brand_tokens = tokens(&brand, dark);

    let differ: Vec<_> = tokens(&copy, dark)
        .into_iter()
        .filter(|token| !brand_tokens.contains(token))
        .collect();

    assert!(!tokens(&copy, dark).is_empty());
    assert_eq!(
        differ,
        vec![],
        "tokens of the copy that brand/liken.css lacks or sets to another value"
    );
}
