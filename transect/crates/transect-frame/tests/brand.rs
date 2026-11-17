//! `src/frame/brand/liken.css` holds the color tokens of `brand/liken.css`
//! at the top of the repository, and no other rule of that file. The
//! rest of the brand's stylesheet sets the measure, the body, and the
//! prose elements of the sites, which would change `transect`'s layout.
//! So `transect` serves a copy of the tokens alone, and this test fails
//! when a token of the copy differs from the brand's value in the same
//! color scheme.

use std::path::Path;

/// The rule that opens the dark scheme in both files.
const DARK: &str = "@media (prefers-color-scheme: dark)";

/// Each `--name: value;` declaration of one color scheme of a
/// stylesheet, in the order the file gives them.
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
    std::fs::read_to_string(path).unwrap_or_else(|error| panic!("{}: {error}", path.display()))
}

#[rstest::rstest]
#[case::light(false)]
#[case::dark(true)]
fn each_copied_token_has_the_brand_value(#[case] dark: bool) {
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let copy = read(&crate_dir.join("src/frame/brand/liken.css"));
    let brand = read(&crate_dir.join("../../../brand/liken.css"));
    let brand_tokens = tokens(&brand, dark);

    let differ: Vec<_> = tokens(&copy, dark)
        .into_iter()
        .filter(|token| !brand_tokens.contains(token))
        .collect();

    assert!(!tokens(&copy, dark).is_empty());
    assert_eq!(differ, vec![], "tokens that brand/liken.css does not hold");
}
