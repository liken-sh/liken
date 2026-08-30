// The binary reads the flags. A bad flag stops the run before a window opens.

use media_browser::browser::Browser;
use media_browser::harness::options::HELP;
use media_browser::harness::{self, Invocation, Options};

fn main() {
    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{HELP}"),
        Ok(Invocation::Run(options)) => {
            if let Err(error) = harness::run(Browser::new(), options) {
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
