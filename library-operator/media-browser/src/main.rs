// The binary reads the flags. A bad flag stops the run before a window opens.

use media_screen::reader::{self, Reader};
use media_screen::{Bus, Wiring};

use media_browser::art::volumes::{self, Volumes};
use media_browser::browser::Browser;
use media_browser::catalog::sidecar::SidecarSource;
use media_browser::catalog::{Source, progress};
use media_browser::harness::options::HELP;
use media_browser::harness::{self, Invocation, Options};
use media_browser::sample;

// The identifier this client connects under, before the hostname the
// crate appends. A broker closes the older connection when two arrive
// under one name, so the idle client and this one carry different
// prefixes on a machine where both run.
const CLIENT_PREFIX: &str = "media-browser";

// The two glibc allocator thresholds, in bytes. A block above the mmap
// threshold comes from mmap and returns to the kernel when it is freed,
// so a page-size decode does not dirty an arena the process keeps. The
// trim threshold returns the top of an arena as soon as that much is
// free. Without the pin, the browser held up to 300 MiB of decode dirt
// on the workstation and gave none of it back.
#[cfg(all(target_os = "linux", target_env = "gnu"))]
const MMAP_THRESHOLD: libc::c_int = 128 * 1024;
#[cfg(all(target_os = "linux", target_env = "gnu"))]
const TRIM_THRESHOLD: libc::c_int = 128 * 1024;

// glibc raises both thresholds on its own as the program frees large
// blocks. Pinning them holds the decode buffers on mmap for the whole
// run.
#[cfg(all(target_os = "linux", target_env = "gnu"))]
fn pin_allocator_thresholds() {
    // mallopt takes two integers and no pointer. A failure leaves the
    // defaults in place, and there is nothing this binary can do about
    // it, so the result is dropped.
    unsafe {
        libc::mallopt(libc::M_MMAP_THRESHOLD, MMAP_THRESHOLD);
        libc::mallopt(libc::M_TRIM_THRESHOLD, TRIM_THRESHOLD);
    }
}

// A build against another libc has no mallopt to call.
#[cfg(not(all(target_os = "linux", target_env = "gnu")))]
fn pin_allocator_thresholds() {}

fn main() {
    // This runs before the flags are parsed, so every allocation after
    // it sees the pinned thresholds.
    pin_allocator_thresholds();

    // The bus wiring is read once here, beside the flags. The crate
    // reads the broker, the Player's name, and every topic of the media
    // tree. The browser's own read takes the app-id, the window grace,
    // and this operator's two topics.
    let wiring = Wiring::from_environment();

    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{HELP}"),
        Ok(Invocation::Run(mut options)) => {
            // The app-id, the window grace, and this operator's two
            // topics are not flags. The display claim delivers the first
            // into the container and the operator sets the rest, so the
            // binary reads them here, after the flags and before the
            // window.
            options.from_environment();
            if let Err(error) = run(*options, &wiring) {
                eprintln!("media-browser: {error}");
                std::process::exit(1);
            }
        }
        Err(error) => {
            eprintln!("media-browser: {error}");
            std::process::exit(2);
        }
    }
}

// A run with a catalog reads the sidecar's file and the volumes the
// library roots name. A run without one browses the invented sample, so the
// client opens on a workstation with no cluster.
fn run(options: Options, wiring: &Wiring) -> Result<(), String> {
    let play_topic = options.play_topic.clone();
    let audience_topic = options.audience_topic.clone();

    let Some(catalog) = options.catalog.clone() else {
        return harness::run(
            Browser::new(sample::Catalog, sample::NoArt)
                .with_page(options.size)
                .with_timing(options.stats.is_some())
                .with_audience(options.people.clone(), options.audience.clone())
                .with_people_file(options.people_file.clone())
                .with_bus(bus(wiring, &audience_topic), play_topic, audience_topic),
            options,
        );
    };

    // A run with no update stream reads the file alone, and a title
    // that lands after it opens waits for the next re-read.
    let updates = options.updates.clone().unwrap_or_default();
    let mut source = SidecarSource::new(catalog, &updates);
    if let Some(progress_file) = options.progress.clone() {
        let stream = options.progress_updates.clone().unwrap_or_default();
        source = source.with_progress(progress_file, &stream);
    }

    // The print is a drill's read of the store, so it runs before the
    // broker connection and before any window.
    if options.print_progress {
        for resume in source.continue_watching(&options.audience) {
            println!("{}", progress::line(&resume));
        }
        return Ok(());
    }

    let roots = options.library_roots.iter().cloned().collect();
    let store = Volumes::with_cache_dir(
        roots,
        volumes::budget(options.size),
        options.cache_dir.clone(),
        options.cache_budget,
    );

    harness::run(
        Browser::new(source, store)
            .with_page(options.size)
            .with_timing(options.stats.is_some())
            .with_audience(options.people.clone(), options.audience.clone())
            .with_people_file(options.people_file.clone())
            .with_bus(bus(wiring, &audience_topic), play_topic, audience_topic),
        options,
    )
}

// The connection the wiring describes. A wiring that names no broker,
// or no topic to read, opens none, and the browser then takes the
// keyboard alone, which is how it runs on a workstation.
//
// The audience topic is the browser's own, so the crate subscribes to it
// on this client's behalf and hands each delivery back. A run the
// operator named no topic for subscribes to none.
fn bus(wiring: &Wiring, audience_topic: &str) -> Option<Box<dyn Bus>> {
    let client_id = reader::client_id(CLIENT_PREFIX, &reader::hostname());
    let topics: Vec<String> = match audience_topic.is_empty() {
        true => Vec::new(),
        false => vec![audience_topic.to_string()],
    };

    Some(Box::new(Reader::open(wiring, &client_id, &topics)?))
}
