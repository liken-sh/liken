//! Loads `examples/` and the knowledge that each component of the
//! repository ships in its own `transect/` directory, with the loader
//! that `--resources` uses.  So an example or an operator's knowledge
//! that fails to load or gets a trouble condition fails a test before a
//! reader copies it or a cluster mounts it.

use std::path::{Path, PathBuf};

use rstest::rstest;

use super::Loaded;
use transect_resources::resources::load;

fn examples() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples")
}

/// The top of the `liken` repository, which contains `transect/` and
/// every other component.
fn repository() -> PathBuf {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../..");
    root.canonicalize().unwrap()
}

/// The `transect/` directory of each component at the top of the
/// repository that has one with YAML in it.  The test finds them by
/// reading the repository, so a new operator's knowledge is checked
/// without a change here.
fn first_party() -> Vec<PathBuf> {
    let components = std::fs::read_dir(repository()).unwrap();
    let mut found: Vec<PathBuf> = components
        .map(|entry| entry.unwrap().path().join("transect"))
        .filter(|directory| has_yaml(directory))
        .collect();
    found.sort();
    found
}

fn has_yaml(directory: &Path) -> bool {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return false;
    };
    entries.map(|entry| entry.unwrap().path()).any(|path| {
        let extension = path.extension().and_then(|extension| extension.to_str());
        matches!(extension, Some("yaml" | "yml"))
    })
}

/// Each fault that loading `directory` finds, one line each: a file
/// that does not load, a document with a trouble condition, a role
/// whose shape neither the directory nor the library has, or a
/// directory with no files.
fn trouble(directory: &Path) -> Vec<String> {
    let loaded: Loaded = load(directory);
    let shown = directory.display();
    let mut trouble = Vec::new();
    if loaded.directory.files.is_empty() {
        trouble.push(format!("{shown} contains no files"));
    }
    for file in &loaded.directory.files {
        if let Some(problem) = &file.problem {
            trouble.push(format!("{shown}: {}: {problem}", file.path));
        }
        for document in &file.documents {
            for condition in document.conditions.iter().filter(|c| c.trouble()) {
                let (path, text) = (&file.path, condition.text());
                let named = format!("{} {}", document.kind, document.name);
                trouble.push(format!("{shown}: {path} {named}: {text}"));
            }
        }
    }
    let shapes = loaded.shapes();
    for known in &loaded.kinds {
        for message in known.clone().draw_with(&shapes) {
            trouble.push(format!("{shown}: {}: {message}", known.name));
        }
    }
    trouble
}

#[rstest]
fn every_example_loads_with_no_condition() {
    let trouble = trouble(&examples());
    assert!(trouble.is_empty(), "{}", trouble.join("\n"));
}

#[rstest]
fn the_knowledge_of_every_component_loads_with_no_condition() {
    let directories = first_party();
    assert!(
        !directories.is_empty(),
        "no directory at the top of {} has a transect/ directory with YAML in it",
        repository().display()
    );
    let trouble: Vec<String> = directories.iter().flat_map(|d| trouble(d)).collect();
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
