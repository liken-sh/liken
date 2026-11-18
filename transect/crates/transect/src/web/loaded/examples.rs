//! Loads `examples/` with the loader that `--resources` uses, so an
//! example that fails to load or gets a trouble condition fails a test
//! before a reader copies it.

use std::path::PathBuf;

use rstest::rstest;

use super::Loaded;
use transect_resources::resources::load;

fn examples() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples")
}

#[rstest]
fn every_example_loads_with_no_condition() {
    let loaded: Loaded = load(&examples());
    let mut trouble = Vec::new();
    for file in &loaded.directory.files {
        if let Some(problem) = &file.problem {
            trouble.push(format!("{}: {problem}", file.path));
        }
        for document in &file.documents {
            for condition in document.conditions.iter().filter(|c| c.trouble()) {
                let named = format!("{} {}", document.kind, document.name);
                trouble.push(format!("{} {named}: {}", file.path, condition.text()));
            }
        }
    }
    assert!(
        !loaded.directory.files.is_empty(),
        "the examples directory {} contains no files",
        examples().display()
    );
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

#[rstest]
fn every_example_project_has_a_readme() {
    let projects = std::fs::read_dir(examples()).unwrap();
    let projects = projects.map(|entry| entry.unwrap().path());
    let missing: Vec<String> = projects
        .filter(|path| path.is_dir() && !path.join("README.md").is_file())
        .map(|path| path.display().to_string())
        .collect();
    assert!(
        missing.is_empty(),
        "these example projects have no README.md: {missing:?}"
    );
}
