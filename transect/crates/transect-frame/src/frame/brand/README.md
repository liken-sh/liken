# brand

`transect` takes its brand from `brand/` at the top of the repository.

- `liken.css` copies only the color tokens of `brand/liken.css`, in
  both color schemes. The rest of that file lays out the sites, and
  `transect` does not use it. `crates/transect-frame/tests/brand.rs` fails when a
  token here differs from the brand's value.
- The two Source Sans 3 fonts are not copied. `web::assets` in the
  `transect` crate embeds `brand/fonts/SourceSans3VF-Upright.otf.woff2`
  and `brand/fonts/SourceSans3VF-Italic.otf.woff2` from `brand/` by
  path. The image includes their license, the SIL Open Font License
  1.1 in `brand/fonts/LICENSE.md`, at
  `/usr/share/doc/source-sans-3/LICENSE.md`.

`transect.svg` is the `transect` mark: three stacked layers with one
line through them, in the greens and the orange of the `liken` brand.
The page serves it as the favicon and shows it beside the name in the
masthead.
