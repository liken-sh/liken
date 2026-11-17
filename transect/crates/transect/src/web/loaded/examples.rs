//! The examples under `examples/`, the knowledge public projects would
//! ship, each loaded through the same loader `--resources` uses.  An
//! example that does not load, or a field renamed in the code and not
//! in the examples, fails here before a reader copies it.

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
        "no examples under {}",
        examples().display()
    );
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

#[rstest]
fn every_project_says_what_it_gives_transect() {
    let projects = std::fs::read_dir(examples()).unwrap();
    let projects = projects.map(|entry| entry.unwrap().path());
    let unsaid: Vec<String> = projects
        .filter(|path| path.is_dir() && !path.join("README.md").is_file())
        .map(|path| path.display().to_string())
        .collect();
    assert!(unsaid.is_empty(), "no README.md in {unsaid:?}");
}
