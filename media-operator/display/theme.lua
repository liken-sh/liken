-- This module holds the look. Every other module draws through it, so the
-- palette, the type scale, and the drawing shapes are defined in one place.
local theme = {}

-- The display draws in one fixed 1920x1080 space. libass scales the whole
-- overlay to the real output, so the same layout serves 720, 1080, and 4K
-- with no branch.
theme.canvas = { w = 1920, h = 1080 }

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

theme.margin = {
  x = 140,
}

-- Place a shape at its top-left with an7 and pos, so the path points are the
-- shape's own local box. p1 is the ASS vector drawing mode.
-- The border is off by default, so a shape draws its fill alone. A shape that
-- passes a width and a color draws an outline whose alpha matches the fill
-- alpha, so the border and the fill dim together.
local function drawing(x, y, color, alpha, path, bord, bordcolor, blur)
  bord = bord or 0
  bordcolor = bordcolor or color
  blur = blur or 0
  return string.format(
    "{\\an7\\pos(%.2f,%.2f)\\bord%.2f\\3c%s\\3a%s\\shad0\\blur%.2f\\1c%s\\1a%s\\p1}%s{\\p0}",
    x, y, bord, bordcolor, alpha, blur, color, alpha, path
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

-- A circle from four cubic beziers. 0.5523 is the control-point offset that
-- fits a bezier arc to a quarter circle.
local function circle_path(r)
  local k = r * 0.5523
  return string.format(
    "m %.2f 0 b %.2f 0 %.2f %.2f %.2f %.2f "
      .. "b %.2f %.2f %.2f %.2f %.2f %.2f "
      .. "b %.2f %.2f 0 %.2f 0 %.2f "
      .. "b 0 %.2f %.2f 0 %.2f 0",
    r, r + k, 2 * r, r - k, 2 * r, r,
    2 * r, r + k, r + k, 2 * r, r, 2 * r,
    r - k, 2 * r, r + k, r,
    r - k, r - k, r
  )
end

function theme.circle(x, y, r, color, alpha)
  return drawing(x, y, color, alpha, circle_path(r))
end

-- A pointy-top regular hexagon in a 2r box, center at (r, r). r is the
-- distance from the center to a vertex. The 0.866 is cos(30 degrees), the
-- half-width of a pointy-top hexagon.
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

-- The scrim holds the contrast, so the label draws flat, with no outline.
function theme.text(x, y, s, size, color, an, alpha)
  alpha = alpha or theme.alpha.opaque
  return string.format(
    "{\\an%d\\pos(%.2f,%.2f)\\fs%d\\bord0\\shad0\\1c%s\\1a%s}%s",
    an, x, y, size, color, alpha, s
  )
end

return theme
