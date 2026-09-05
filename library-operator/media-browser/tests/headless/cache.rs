// The poster counts cross the browser and harness into the statistics file.

use super::*;

fn cached_catalog_run(
    dir: &Path,
    database: &Path,
    volume: &Path,
    cache: &Path,
) -> (Run, serde_json::Value) {
    let frames = dir.join("frames");
    let stats = dir.join("stats.json");
    let run = headless(
        dir,
        &[
            "--catalog",
            &text(database),
            "--updates",
            "http://127.0.0.1:1",
            "--library-root",
            &format!("drill/films={}", text(volume)),
            "--cache-dir",
            &text(cache),
            "--stats",
            &text(&stats),
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );
    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.00.png"), &run);
    let measured = measurements(&stats, &run);
    (run, measured)
}

#[test]
fn catalog_runs_report_source_decodes_then_disk_cache_hits() {
    let fixture_dir = workspace("poster-counts-fixture");
    let (database, volume) = fixture(&fixture_dir);
    let cache = fixture_dir.join("cache");

    let first_dir = workspace("poster-counts-first");
    let (_first, first_counts) = cached_catalog_run(&first_dir, &database, &volume, &cache);
    assert!(
        first_counts["posters_from_source"].as_u64().unwrap_or(0) > 0,
        "{first_counts}"
    );
    assert_eq!(first_counts["posters_from_cache"], serde_json::json!(0));

    let second_dir = workspace("poster-counts-second");
    let (_second, second_counts) = cached_catalog_run(&second_dir, &database, &volume, &cache);
    assert!(
        second_counts["posters_from_cache"].as_u64().unwrap_or(0) > 0,
        "{second_counts}"
    );
    assert_eq!(second_counts["posters_from_source"], serde_json::json!(0));
}
