// Starts the headless Chromium that pages.mjs and behave.mjs drive.
//
// The repository pins no copy of Playwright, so you install
// playwright-core into a scratch directory and name it with the
// environment variable PLAYWRIGHT_CORE, the directory of the package.
// CHROMIUM names the browser binary, usually the build that
// `npx playwright-core install chromium` puts in ~/.cache/ms-playwright
// for that version of playwright-core.  ../htmx.md gives the commands.

import path from "node:path";
import { pathToFileURL } from "node:url";

function required(name, what) {
  const value = process.env[name];
  if (!value) {
    console.error(`${name} is not set.  Set it to ${what}, as ../htmx.md shows.`);
    process.exit(2);
  }
  return value;
}

export async function launch() {
  const module = required("PLAYWRIGHT_CORE", "the directory of the playwright-core package");
  const binary = required("CHROMIUM", "the path of the Chromium binary that playwright-core drives");
  let playwright;
  try {
    playwright = await import(pathToFileURL(path.join(module, "index.mjs")).href);
  } catch (error) {
    console.error(`PLAYWRIGHT_CORE=${module} has no playwright-core: ${error.message}`);
    process.exit(2);
  }
  try {
    return await playwright.chromium.launch({ executablePath: binary });
  } catch (error) {
    console.error(`CHROMIUM=${binary} did not start: ${error.message}`);
    process.exit(2);
  }
}

// The arguments of each script: the server's base URL and a directory
// for the screenshots and the reports.
export function args(script) {
  const [base, out] = process.argv.slice(2);
  if (!base || !out) {
    console.error(`usage: node ${script} <base url, such as http://127.0.0.1:18432> <output directory>`);
    process.exit(2);
  }
  return { base: base.replace(/\/$/, ""), out };
}
