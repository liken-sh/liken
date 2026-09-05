use super::*;

fn args(line: &str) -> Vec<String> {
    line.split_whitespace().map(str::to_string).collect()
}

#[test]
fn no_flags_is_a_run_with_the_defaults() {
    assert_eq!(
        Options::parse(Vec::new()),
        Ok(Invocation::Run(Box::default()))
    );
}

#[test]
fn the_default_size_is_1920x1080_and_the_cache_is_off() {
    let options = Options::default();
    assert_eq!(options.size, (1920, 1080));
    assert_eq!(options.cache_dir, None);
}

#[test]
fn help_stops_the_parse() {
    assert_eq!(Options::parse(args("--help")), Ok(Invocation::Help));
    assert_eq!(
        Options::parse(args("--size 800x600 --help")),
        Ok(Invocation::Help)
    );
}

#[test]
fn help_names_the_poster_cache_directory() {
    assert!(HELP.contains("--cache-dir PATH"));
}

#[test]
fn an_unknown_flag_is_an_error() {
    assert_eq!(
        Options::parse(args("--posters")),
        Err("unknown flag --posters".to_string())
    );
}

#[test]
fn a_stats_flag_without_its_value_is_an_error() {
    assert_eq!(
        Options::parse(args("--stats")),
        Err("--stats needs a value".to_string())
    );
}

#[test]
fn a_cache_directory_without_its_value_is_an_error() {
    assert_eq!(
        Options::parse(args("--cache-dir")),
        Err("--cache-dir needs a value".to_string())
    );
}

#[test]
fn the_flags_land_in_the_options() {
    let Ok(Invocation::Run(options)) = Options::parse(args(
        "--script 1.0:p --capture /frames --capture-at 0.5 \
         --stats /stats.json --cache-dir /cache --quit-after 3 --size 1280x720",
    )) else {
        panic!("the flags parse");
    };

    assert_eq!(options.script, vec![(1.0, "p".to_string())]);
    assert_eq!(options.capture_dir, Some(PathBuf::from("/frames")));
    assert_eq!(options.capture_at, vec![0.5]);
    assert_eq!(options.stats, Some(PathBuf::from("/stats.json")));
    assert_eq!(options.cache_dir, Some(PathBuf::from("/cache")));
    assert_eq!(options.quit_after, Some(3.0));
    assert_eq!(options.size, (1280, 720));
}

#[test]
fn the_last_cache_directory_overrides_the_first() {
    let Ok(Invocation::Run(options)) =
        Options::parse(args("--cache-dir /default --cache-dir /chosen"))
    else {
        panic!("the cache directories parse");
    };

    assert_eq!(options.cache_dir, Some(PathBuf::from("/chosen")));
}

#[test]
fn the_catalog_flags_land_in_the_options() {
    let Ok(Invocation::Run(options)) = Options::parse(args(
        "--catalog /state/state.db --updates http://127.0.0.1:20081 \
         --library-root local/movies=/films --library-root local/series=/shows",
    )) else {
        panic!("the flags parse");
    };

    assert_eq!(options.catalog, Some(PathBuf::from("/state/state.db")));
    assert_eq!(options.updates, Some("http://127.0.0.1:20081".to_string()));
    assert_eq!(
        options.library_roots,
        vec![
            ("local/movies".to_string(), PathBuf::from("/films")),
            ("local/series".to_string(), PathBuf::from("/shows")),
        ]
    );
}

#[test]
fn the_catalog_flags_are_all_optional() {
    let Ok(Invocation::Run(options)) = Options::parse(Vec::new()) else {
        panic!("no flags parse");
    };

    assert_eq!(options.catalog, None);
    assert_eq!(options.updates, None);
    assert!(options.library_roots.is_empty());
}

#[test]
fn a_stream_or_a_volume_without_a_catalog_is_an_error() {
    assert_eq!(
        Options::parse(args("--updates http://127.0.0.1:20081")),
        Err("--updates needs --catalog".to_string())
    );
    assert_eq!(
        Options::parse(args("--library-root local/movies=/films")),
        Err("--library-root needs --catalog".to_string())
    );
}

#[test]
fn a_library_root_is_a_name_and_a_path_around_an_equals() {
    assert_eq!(
        parse_root("local/movies=/films"),
        Ok(("local/movies".to_string(), PathBuf::from("/films")))
    );
    assert_eq!(
        parse_root("local/movies"),
        Err("bad --library-root local/movies".to_string())
    );
    assert!(parse_root("=/films").is_err());
    assert!(parse_root("local/movies=").is_err());
}

fn environment(pairs: &[(&str, &str)]) -> Options {
    let pairs: Vec<(String, String)> = pairs
        .iter()
        .map(|(name, value)| (name.to_string(), value.to_string()))
        .collect();
    let mut options = Options::default();
    options.read_environment(|name| {
        pairs
            .iter()
            .find(|(set, _)| set == name)
            .map(|(_, value)| value.clone())
    });
    options
}

#[test]
fn an_empty_environment_names_no_screen_and_arms_nothing() {
    let options = environment(&[]);
    assert_eq!(options.app_id, "");
    assert_eq!(options.window_grace, None);
}

#[test]
fn an_empty_environment_names_no_play_topic() {
    assert_eq!(environment(&[]).play_topic, "");
}

#[test]
fn this_operator_names_the_play_topic() {
    let options = environment(&[(PLAY_TOPIC, "liken/library/players/house/den-tv/play")]);

    assert_eq!(
        options.play_topic,
        "liken/library/players/house/den-tv/play"
    );
}

#[test]
fn the_claim_names_the_screen_and_the_operator_arms_the_watchdog() {
    let options = environment(&[(APP_ID, "media-den-tv"), (WINDOW_GRACE, "15")]);
    assert_eq!(options.app_id, "media-den-tv");
    assert_eq!(options.window_grace, Some(Duration::from_secs(15)));
}

#[test]
fn a_positive_grace_arms_the_watchdog() {
    assert_eq!(grace("15"), Some(Duration::from_secs(15)));
    assert_eq!(grace(" 1.5 "), Some(Duration::from_secs_f64(1.5)));
}

#[test]
fn anything_but_a_positive_grace_leaves_it_off() {
    assert_eq!(grace(""), None);
    assert_eq!(grace("0"), None);
    assert_eq!(grace("-5"), None);
    assert_eq!(grace("soon"), None);
    assert_eq!(grace("inf"), None);
}

#[test]
fn a_script_sorts_by_time() {
    assert_eq!(
        parse_script("4.0:o, 1.5:p,0.25:down"),
        Ok(vec![
            (0.25, "down".to_string()),
            (1.5, "p".to_string()),
            (4.0, "o".to_string()),
        ])
    );
}

#[test]
fn an_empty_script_is_empty() {
    assert_eq!(parse_script(""), Ok(Vec::new()));
    assert_eq!(parse_script(" , "), Ok(Vec::new()));
}

#[test]
fn a_script_step_needs_a_time_and_a_key() {
    assert!(parse_script("p").is_err());
    assert!(parse_script("later:p").is_err());
    assert!(parse_script("1.0:").is_err());
}

#[test]
fn capture_times_sort() {
    assert_eq!(parse_times("3.2, 0.5 ,1"), Ok(vec![0.5, 1.0, 3.2]));
}

#[test]
fn a_capture_time_must_be_a_number() {
    assert_eq!(
        parse_times("0.5,soon"),
        Err("bad capture time soon".to_string())
    );
}

#[test]
fn a_size_is_two_numbers_around_an_x() {
    assert_eq!(parse_size("1920x1080"), Ok((1920, 1080)));
    assert_eq!(parse_size(" 640x480 "), Ok((640, 480)));
    assert!(parse_size("1920").is_err());
    assert!(parse_size("widexhigh").is_err());
}
