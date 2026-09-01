// The binary reads the flags. A bad flag stops the run before a window opens.

use media_browser::browser::Browser;
use media_browser::bus::Bus;
use media_browser::bus::reader::{self, Reader};
use media_browser::catalog::sidecar::SidecarSource;
use media_browser::harness::options::HELP;
use media_browser::harness::{self, Invocation, Options};
use media_browser::posters::volumes::{self, Volumes};
use media_browser::sample;

fn main() {
    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{HELP}"),
        Ok(Invocation::Run(mut options)) => {
            // The app-id, the window grace, and the bus are not flags.
            // The display claim delivers the first into the container and
            // the operator sets the rest from the Player's status, so the
            // binary reads them here, after the flags and before the window.
            options.from_environment();
            if let Err(error) = run(*options) {
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
    let bus = bus(&options);

    let Some(catalog) = options.catalog.clone() else {
        return harness::run(
            Browser::new(sample::Catalog, sample::NoArt).with_bus(bus),
            options,
        );
    };

    // A run with no update stream reads the file alone, and a title
    // that lands after it opens waits for the next re-read.
    let updates = options.updates.clone().unwrap_or_default();
    let source = SidecarSource::new(catalog, &updates);
    let roots = options.library_roots.iter().cloned().collect();
    let posters = Volumes::new(roots, volumes::budget(options.size.0));

    harness::run(Browser::new(source, posters).with_bus(bus), options)
}

// The connection the three variables describe. A run that misses one of
// them opens none, and the browser then takes the keyboard alone.
fn bus(options: &Options) -> Option<Box<dyn Bus>> {
    let client_id = reader::client_id(&reader::hostname());
    let reader = Reader::open(
        &options.bus_address,
        &client_id,
        options.commands_topic.clone(),
        options.screen_topic.clone(),
    )?;

    Some(Box::new(reader))
}
