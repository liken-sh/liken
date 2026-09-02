// The binary reads the flags. A bad flag stops the run before a window opens.

use media_screen::reader::{self, Reader};
use media_screen::{Bus, Wiring};

use media_browser::browser::Browser;
use media_browser::catalog::sidecar::SidecarSource;
use media_browser::harness::options::HELP;
use media_browser::harness::{self, Invocation, Options};
use media_browser::posters::volumes::{self, Volumes};
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
    // reads the broker, the Player's name, and every topic. The
    // browser's own read takes the app-id, the window grace, and the
    // play topic.
    let wiring = Wiring::from_environment();

    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{HELP}"),
        Ok(Invocation::Run(mut options)) => {
            // The app-id, the window grace, and the play topic are not
            // flags. The display claim delivers the first into the
            // container and the operator sets the rest, so the binary
            // reads them here, after the flags and before the window.
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
    let bus = bus(wiring);
    let play_topic = options.play_topic.clone();

    let Some(catalog) = options.catalog.clone() else {
        return harness::run(
            Browser::new(sample::Catalog, sample::NoArt)
                .with_page(options.size)
                .with_bus(bus, play_topic),
            options,
        );
    };

    // A run with no update stream reads the file alone, and a title
    // that lands after it opens waits for the next re-read.
    let updates = options.updates.clone().unwrap_or_default();
    let source = SidecarSource::new(catalog, &updates);
    let roots = options.library_roots.iter().cloned().collect();
    let posters = Volumes::new(roots, volumes::budget(options.size));

    harness::run(
        Browser::new(source, posters)
            .with_page(options.size)
            .with_bus(bus, play_topic),
        options,
    )
}

// The connection the wiring describes. A wiring that names no broker,
// or no topic to read, opens none, and the browser then takes the
// keyboard alone, which is how it runs on a workstation.
fn bus(wiring: &Wiring) -> Option<Box<dyn Bus>> {
    let client_id = reader::client_id(CLIENT_PREFIX, &reader::hostname());

    Some(Box::new(Reader::open(wiring, &client_id)?))
}
