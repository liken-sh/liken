// The binary reads the flags. A bad flag stops the run before a window opens.

use media_browser::browser::Browser;
use media_browser::catalog::sidecar::SidecarSource;
use media_browser::harness::options::HELP;
use media_browser::harness::{self, Invocation, Options};
use media_browser::posters::volumes::{self, Volumes};
use media_browser::sample;

fn main() {
    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{HELP}"),
        Ok(Invocation::Run(options)) => {
            if let Err(error) = run(options) {
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
fn run(options: Options) -> Result<(), String> {
    let Some(catalog) = options.catalog.clone() else {
        return harness::run(Browser::new(sample::Catalog, sample::NoArt), options);
    };

    // A run with no update stream reads the file alone, and a title
    // that lands after it opens waits for the next re-read.
    let updates = options.updates.clone().unwrap_or_default();
    let source = SidecarSource::new(catalog, &updates);
    let roots = options.library_roots.iter().cloned().collect();
    let posters = Volumes::new(roots, volumes::budget(options.size.0));

    harness::run(Browser::new(source, posters), options)
}
