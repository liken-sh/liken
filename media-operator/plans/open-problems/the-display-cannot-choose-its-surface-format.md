# The display cannot choose its surface format

Open problem. The display in a playback pod draws the OSD over the
film, and the compositor blends the display's surface into the frame by
its alpha. The scrims behind the header and the scrubber fade from an
alpha of 0.796 to clear, so the surface format needs an alpha channel
with enough levels to make that fade smooth.

## What happens now

The display renders through iced 0.14. `iced_wgpu` asks the surface for
its formats, filters out the ones that need a wgpu feature, and takes
the first one without sRGB, because the display builds iced with
`web-colors` (`iced_wgpu-0.14.0/src/window/compositor.rs`). The program
has no setting that changes the choice.

On the Debian sid snapshot of 2026-10-24, Mesa 26.2.5's Wayland
swapchain lists its 10-bit formats before its 8-bit ones. On `liken-1`'s
`stick-1`, Weston's scene dump showed the display's surface as
`ABGR2101010`. That format's alpha has 2 bits, which gives four levels,
and each scrim drew as two flat bands. On an Intel Arc GPU with the
same Mesa, `vulkaninfo` lists `R16G16B16A16_SFLOAT`,
`R16G16B16A16_UNORM`, `A2R10G10B10_UNORM_PACK32`, and
`A2B10G10R10_UNORM_PACK32` before `B8G8R8A8_UNORM`.

The display image sets `vk_wsi_force_bgra8_unorm_first=true`
(`media-operator/Dockerfile.screen`). Mesa reads the driconf option
from the environment and lists `B8G8R8A8_UNORM` first, and iced takes
it. With the option set, `vulkaninfo` on the same Intel Arc GPU lists
`B8G8R8A8_UNORM` first.

## Why it is not finished

The option is specific to Mesa. It also always chooses 8-bit color,
even on a GPU that offers a 16-bit format with full alpha, where the
display could keep more than 8 bits of color and still blend smoothly.

## Possible remedies

- **A format hook in iced.** The display would choose by a rule: the
  format with the most alpha bits, then the most color bits. This
  needs a change upstream in `iced_wgpu`, which the display then takes
  in a release.
- **A patched `iced_wgpu` in this repository.** A `[patch.crates-io]`
  copy that applies the same rule. It fixes the display now, but every
  iced upgrade has to carry the patch forward.
- **Keep the Mesa option.** It works on every GPU that `liken` drives
  today, because each one uses Mesa. It is the current state.

The first remedy is the one to pursue. When iced has the hook, the
display chooses its format and the image drops the option.
