-- This module holds the look. Every other module draws through it, so the
-- palette, the type scale, and the drawing shapes are defined in one place.
local theme = {}

-- The display draws in one space 1080 rows tall. libass scales the whole
-- overlay to the real output, so the same layout serves 720, 1080, and 4K
-- with no branch.
-- The width follows the real surface's own ratio, so a canvas pixel is
-- square. A fixed 1920 canvas on a 21:9 screen stretched every vector
-- drawing by a third and pulled every margin inside where it belonged. A
-- 16:9 surface gives 1920, the width this space always held, so a 16:9
-- screen draws every number it drew before.
theme.canvas = { w = 1920, h = 1080 }

-- update_canvas takes the width from the surface mpv reports. Every module
-- reads theme.canvas at draw time, because a width captured at load would
-- hold the width of another screen. main calls this before it tells the
-- modules the size changed.
function theme.update_canvas()
  local d = mp.get_property_native("osd-dimensions")
  if not d or not d.w or d.w <= 0 or not d.h or d.h <= 0 then
    return
  end
  theme.canvas.w = math.floor(theme.canvas.h * d.w / d.h + 0.5)
end

-- osd_scale reads how the canvas maps to the real screen. sx maps a canvas x to
-- real pixels, and sy maps a canvas y. It is nil until a frame has rendered and
-- mpv reports a size.
-- The canvas takes its width from the same surface, so the two factors come
-- out equal. A caller that places a bitmap still reads the axis it means.
function theme.osd_scale()
  local d = mp.get_property_native("osd-dimensions")
  if not d or not d.w or d.w <= 0 or not d.h or d.h <= 0 then
    return nil
  end
  return { sx = d.w / theme.canvas.w, sy = d.h / theme.canvas.h, w = d.w, h = d.h }
end

-- An ASS color is BGR hex, the byte order libass reads, not RGB. The values
-- are liken brand tokens from the brand theme's liken.css. The accent fill is
-- the dark-scheme lichen green --link, the text is --ink, and the muted grey
-- is --ink-muted. The bar track is the light-scheme --link, a deep green dark
-- enough that the bright elapsed fill reads over it.
theme.color = {
  text = "&HE8E8E8&",
  fill = "&H9AC4B4&",
  track = "&H3A5D4A&",
  muted = "&HADA6A0&",
  playhead = "&H9AC4B4&",
  shadow = "&H000000&",
}

-- The global fade factor, from 0 clear to 1 full. drawing and theme.text scale
-- every alpha by it, so the whole OSD fades as one. At 1 the alpha is unchanged.
theme.fade = 1

function theme.set_fade(v)
  theme.fade = v
end

-- faded scales one ASS alpha byte by the fade factor. The byte runs 00 opaque to
-- FF transparent, and out = 255 - round(fade * (255 - in)). At fade 1 the output
-- byte equals the input, so a full OSD draws as it does with no fade at all.
local function faded(alpha)
  local hex = alpha:match("&H(%x+)&")
  if not hex then
    return alpha
  end
  local t = tonumber(hex, 16)
  local out = 255 - math.floor(theme.fade * (255 - t) + 0.5)
  return string.format("&H%02X&", out)
end

-- faded_alpha is faded for a module that sets an alpha inline. It fades an inline
-- override with the rest of the OSD, so a segment with its own alpha does not hold
-- that alpha while everything around it fades.
function theme.faded_alpha(alpha)
  return faded(alpha)
end

-- The fade timing lives here because two things fade on clocks of their own,
-- the OSD and the volume indicator, and the two must look the same. A fade
-- takes fade_in_ms to reach full and fade_out_ms to reach clear, and the out
-- is longer than the in, so anything on the display leaves more slowly than
-- it arrives.
theme.fade_in_ms = 350
theme.fade_out_ms = 600
-- A fade steps on this period, about sixty times a second, and requests a
-- redraw on each step.
theme.fade_tick = 1 / 60
-- An element the display summons for one action leaves this many seconds
-- after the last one. The OSD and the volume indicator wait out the same
-- window, each on its own timer.
theme.idle_hide = 4

-- An ASS alpha runs from 00, opaque, to FF, transparent.
theme.alpha = {
  opaque = "&H00&",
  subdued = "&H80&",
  track = "&H50&",
  dim = "&HA8&",
  panel = "&H14&",
  highlight = "&H30&",
}

-- The type scale, in canvas pixels. The sizes are large enough to read from
-- a couch at 1080.
theme.type = {
  title = 64,
  label = 40,
  small = 34,
  tiny = 28,
}

-- The layout margins and the shared rows. x is the side margin every
-- flush-left and flush-right element keeps. y is the top margin, and by
-- symmetry the bottom one: the clock, the header, and the activity line
-- hang from it, and the identity block and the preview legend stand the
-- same distance off the bottom edge. bar_y is the scrubber bar's center
-- line, where the image counter also sits. panel_bottom is the baseline a
-- chooser or an adjuster panel grows upward from. They live here because
-- two modules that share a row must read one number, not keep two copies
-- that happen to agree.
theme.margin = {
  x = 140,
  y = 90,
}
theme.bar_y = 904
theme.panel_bottom = 876
-- line_pitch is the drop from one line of the top-right column to the next.
-- The clock hangs at the top margin, the activity line one pitch under it,
-- and the volume row one pitch under that, so the three read as a column and
-- no two of them touch.
theme.line_pitch = theme.type.small + 12

-- The whole display draws in one family. libass resolves it through fontconfig,
-- and the player image installs the font, so the text renders the same on every
-- machine. With no installed match, libass falls back and the look drifts.
theme.font = "Source Sans 3"

-- Place a shape at its top-left with an7 and pos, so the path points are the
-- shape's own local box. p1 is the ASS vector drawing mode.
-- The border is off by default, so a shape draws its fill alone. A shape that
-- passes a width and a color draws an outline whose alpha matches the fill
-- alpha, so the border and the fill dim together.
local function drawing(x, y, color, alpha, path, bord, bordcolor, blur)
  bord = bord or 0
  bordcolor = bordcolor or color
  blur = blur or 0
  local a = faded(alpha)
  return string.format(
    "{\\an7\\pos(%.2f,%.2f)\\bord%.2f\\3c%s\\3a%s\\shad0\\blur%.2f\\1c%s\\1a%s\\p1}%s{\\p0}",
    x, y, bord, bordcolor, a, blur, color, a, path
  )
end

function theme.rect(x, y, w, h, color, alpha)
  local path = string.format("m 0 0 l %.2f 0 l %.2f %.2f l 0 %.2f", w, w, h, h)
  return drawing(x, y, color, alpha, path)
end

-- The heights of the scrim's top band and bottom band, in canvas pixels.
theme.scrim_top_h = 410
theme.scrim_bottom_h = 480
-- The scrim's dark plateau, as an ASS alpha. 0 is opaque, 255 is clear.
theme.scrim_edge_alpha = 0x34
-- The dark plateau covers this fraction of the scrim height, at the screen
-- edge, over the text. The blur softens its inner edge.
theme.scrim_solid = 0.66
-- How far the blur carries the fade inward, as a fraction of the scrim height.
theme.scrim_reach = 0.3

-- theme.scrim draws the dark gradient behind a cluster of text, so the text
-- reads against one background whatever the frame behind it. edge names the
-- dark side, top or bottom, and the far side fades to clear.
--
-- The scrim is one blurred shape, not a stack of translucent bands. Tiled bands
-- meet at shared edges, and a wide film that squishes the tall canvas lands
-- those edges sub-pixel, where they alias into fine lines. One blurred shape has
-- no interior edges, so it stays smooth at any output scale. A dark plateau
-- covers the text at the screen edge, and the blur carries its inner edge to
-- clear. The rectangle bleeds past the screen on every side but the inner one
-- by the blur width, so the blur there falls off the screen. Only the inner
-- edge fades, and the very edge stays fully dark.
function theme.scrim(x, y, w, h, edge)
  local solid = h * theme.scrim_solid
  local blur = h * theme.scrim_reach
  local ry
  if edge == "bottom" then
    ry = y + h - solid
  else
    ry = y - blur
  end
  local rh = solid + blur
  local rx = x - blur
  local rw = w + 2 * blur
  local path = string.format("m 0 0 l %.2f 0 l %.2f %.2f l 0 %.2f", rw, rw, rh, rh)
  return drawing(
    rx, ry, theme.color.shadow, string.format("&H%02X&", theme.scrim_edge_alpha), path, 0, nil, blur
  )
end

local function rounded_path(w, h, r)
  r = math.min(r, w / 2, h / 2)
  return string.format(
    "m %.2f 0 l %.2f 0 b %.2f 0 %.2f 0 %.2f %.2f l %.2f %.2f "
      .. "b %.2f %.2f %.2f %.2f %.2f %.2f l %.2f %.2f "
      .. "b 0 %.2f 0 %.2f 0 %.2f l 0 %.2f b 0 0 0 0 %.2f 0",
    r, w - r, w, w, w, r, w, h - r,
    w, h, w, h, w - r, h, r, h,
    h, h, h - r, r, r
  )
end

function theme.rounded_rect(x, y, w, h, r, color, alpha)
  return drawing(x, y, color, alpha, rounded_path(w, h, r))
end

-- A menu panel: a faint dark fill inside a solid green border, so a chooser or
-- an adjuster reads as one surface over the video. The border is liken's green,
-- and the fill dims the video behind the text without hiding it.
function theme.panel(x, y, w, h)
  return string.format(
    "{\\an7\\pos(%.2f,%.2f)\\bord2\\3c%s\\3a&H00&\\shad0\\1c%s\\1a%s\\p1}%s{\\p0}",
    x, y, theme.color.fill, theme.color.shadow, theme.alpha.panel, rounded_path(w, h, 14)
  )
end

-- A pointy-top regular hexagon in a 2r box, center at (r, r). r is the
-- distance from the center to a vertex. The 0.866 is cos(30 degrees), the
-- half-width of a pointy-top hexagon.
-- The shape needs no counter-stretch. The canvas holds the screen's own
-- ratio, so a hexagon drawn regular on the canvas lands regular on the
-- screen.
local function hexagon_path(r)
  local a = 0.8660254 * r
  return string.format(
    "m %.2f 0 l %.2f %.2f l %.2f %.2f l %.2f %.2f l %.2f %.2f l %.2f %.2f",
    r, r + a, 0.5 * r, r + a, 1.5 * r, r, 2 * r, r - a, 1.5 * r, r - a, 0.5 * r
  )
end

function theme.hexagon(x, y, r, color, alpha, bord, bordcolor)
  return drawing(x, y, color, alpha, hexagon_path(r), bord, bordcolor)
end

-- theme.shape draws one ASS path the caller supplies, for a mark the named
-- shapes above do not cover, such as the volume glyph. The path is in the
-- shape's own local box, and the shape takes the same fade every other shape
-- takes. A border width and a color draw an outline, so a mark over another
-- mark in the same color reads apart from it.
function theme.shape(x, y, path, color, alpha, bord, bordcolor)
  return drawing(x, y, color, alpha, path, bord, bordcolor)
end

-- The scrim holds the contrast, so the label draws flat, with no outline.
function theme.text(x, y, s, size, color, an, alpha)
  alpha = alpha or theme.alpha.opaque
  return string.format(
    "{\\an%d\\pos(%.2f,%.2f)\\fn%s\\fs%d\\bord0\\shad0\\1c%s\\1a%s}%s",
    an, x, y, theme.font, size, color, faded(alpha), s
  )
end

return theme
