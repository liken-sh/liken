-- The liken mark, the centered element of the idle screen. It draws the brand
-- logo, a mosaic of fourteen hexagons, so a person reads which system the unit
-- belongs to while no film runs. The player image ships no brand asset, so the
-- hexagon points and fills are embedded here and drawn as ASS vector shapes.
-- The source is the brand repository's liken.svg, in the viewBox -41 -41 82 82.
local theme = require("theme")

local logo = {}

-- The fourteen hexagons, in the SVG's own coordinate space. Each holds its six
-- vertices, its fill color as an RGB hex, and the stroke width that the SVG
-- draws in the same color to round the corners. The colors are the seven brand
-- greens and the one orange.
local HEXAGONS = {
  { fill = "#6e8352", stroke = 1.40, points = { { -4.33, -9.27 }, { 2.77, -5.17 }, { 2.77, 3.03 }, { -4.33, 7.13 }, { -11.43, 3.03 }, { -11.43, -5.17 } } },
  { fill = "#4a5d3a", stroke = 1.40, points = { { 12.99, -9.27 }, { 20.09, -5.17 }, { 20.09, 3.03 }, { 12.99, 7.13 }, { 5.89, 3.03 }, { 5.89, -5.17 } } },
  { fill = "#93a877", stroke = 1.40, points = { { 4.33, -24.27 }, { 11.43, -20.17 }, { 11.43, -11.97 }, { 4.33, -7.87 }, { -2.77, -11.97 }, { -2.77, -20.17 } } },
  { fill = "#93a877", stroke = 1.40, points = { { -12.99, -24.27 }, { -5.89, -20.17 }, { -5.89, -11.97 }, { -12.99, -7.87 }, { -20.09, -11.97 }, { -20.09, -20.17 } } },
  { fill = "#b4c49a", stroke = 1.40, points = { { -21.65, -9.27 }, { -14.55, -5.17 }, { -14.55, 3.03 }, { -21.65, 7.13 }, { -28.75, 3.03 }, { -28.75, -5.17 } } },
  { fill = "#4a5d3a", stroke = 1.40, points = { { -12.99, 5.73 }, { -5.89, 9.83 }, { -5.89, 18.03 }, { -12.99, 22.13 }, { -20.09, 18.03 }, { -20.09, 9.83 } } },
  { fill = "#93a877", stroke = 1.40, points = { { 4.33, 5.73 }, { 11.43, 9.83 }, { 11.43, 18.03 }, { 4.33, 22.13 }, { -2.77, 18.03 }, { -2.77, 9.83 } } },
  { fill = "#e0872f", stroke = 1.19, points = { { 21.65, -23.04 }, { 27.69, -19.56 }, { 27.69, -12.59 }, { 21.65, -9.10 }, { 15.61, -12.59 }, { 15.61, -19.56 } } },
  { fill = "#b4c49a", stroke = 0.98, points = { { -4.33, -36.81 }, { 0.64, -33.94 }, { 0.64, -28.20 }, { -4.33, -25.33 }, { -9.30, -28.20 }, { -9.30, -33.94 } } },
  { fill = "#93a877", stroke = 0.77, points = { { 12.99, -35.58 }, { 16.90, -33.33 }, { 16.90, -28.82 }, { 12.99, -26.56 }, { 9.08, -28.82 }, { 9.08, -33.33 } } },
  { fill = "#6e8352", stroke = 1.12, points = { { -30.31, 7.37 }, { -24.63, 10.65 }, { -24.63, 17.21 }, { -30.31, 20.49 }, { -35.99, 17.21 }, { -35.99, 10.65 } } },
  { fill = "#b4c49a", stroke = 0.91, points = { { -4.33, 23.60 }, { 0.29, 26.26 }, { 0.29, 31.59 }, { -4.33, 34.26 }, { -8.95, 31.59 }, { -8.95, 26.26 } } },
  { fill = "#4a5d3a", stroke = 1.12, points = { { 12.99, 22.37 }, { 18.67, 25.65 }, { 18.67, 32.21 }, { 12.99, 35.49 }, { 7.31, 32.21 }, { 7.31, 25.65 } } },
  { fill = "#b4c49a", stroke = 1.26, points = { { 21.65, 6.55 }, { 28.04, 10.24 }, { 28.04, 17.62 }, { 21.65, 21.31 }, { 15.26, 17.62 }, { 15.26, 10.24 } } },
}

-- An ASS color is BGR hex, the byte order libass reads. An SVG color is RGB, so
-- convert #RRGGBB to &HBBGGRR& by swapping the red and blue bytes.
local function ass_color(rgb)
  local r, g, b = rgb:match("#(%x%x)(%x%x)(%x%x)")
  return "&H" .. b .. g .. r .. "&"
end

-- The bounding box of every vertex, in SVG coordinates. The mark maps to the
-- canvas from this box, so a change to the embedded points needs no other edit.
local function bounds()
  local minx, miny = math.huge, math.huge
  local maxx, maxy = -math.huge, -math.huge
  for _, hex in ipairs(HEXAGONS) do
    for _, p in ipairs(hex.points) do
      minx = math.min(minx, p[1])
      maxx = math.max(maxx, p[1])
      miny = math.min(miny, p[2])
      maxy = math.max(maxy, p[2])
    end
  end
  return minx, miny, maxx, maxy
end

local MINX, MINY, MAXX, MAXY = bounds()

-- The mark fills the middle third of the canvas width, centered. The scale maps
-- the bounding box width to one third of the fixed canvas, and the same factor
-- scales the height, so the aspect ratio holds. The offset places the box
-- center at the canvas center. The SVG y axis and the ASS y axis both run down,
-- so the shape needs no flip. libass then scales the whole overlay to the real
-- screen.
local SCALE = (theme.canvas.w / 3) / (MAXX - MINX)
local BOX_CX = (MINX + MAXX) / 2
local BOX_CY = (MINY + MAXY) / 2

local function to_canvas_x(x)
  return theme.canvas.w / 2 + (x - BOX_CX) * SCALE
end

local function to_canvas_y(y)
  return theme.canvas.h / 2 + (y - BOX_CY) * SCALE
end

-- Build each hexagon's static parts once: the ASS path in canvas coordinates,
-- the fill color, and the border width. The border is the SVG stroke, scaled
-- and drawn in the fill color, so the corners round the way the SVG rounds them.
-- The geometry never changes between frames, so only the alpha is left to the
-- draw.
local shapes = {}
for _, hex in ipairs(HEXAGONS) do
  local segs = {}
  for index, p in ipairs(hex.points) do
    local verb = index == 1 and "m" or "l"
    segs[#segs + 1] = string.format("%s %.2f %.2f", verb, to_canvas_x(p[1]), to_canvas_y(p[2]))
  end
  shapes[#shapes + 1] = {
    path = table.concat(segs, " "),
    color = ass_color(hex.fill),
    bord = hex.stroke * SCALE / 2,
  }
end

-- logo.draw returns the ASS for the whole mark, one drawing per hexagon. The
-- points are absolute canvas coordinates, so each shape anchors at the origin
-- with an7 and pos(0,0). The mark draws opaque, and theme.faded_alpha scales
-- that alpha by the OSD fade so the mark fades with the rest of the screen.
function logo.draw()
  local alpha = theme.faded_alpha(theme.alpha.opaque)
  local parts = {}
  for _, s in ipairs(shapes) do
    parts[#parts + 1] = string.format(
      "{\\an7\\pos(0,0)\\bord%.2f\\3c%s\\3a%s\\shad0\\1c%s\\1a%s\\p1}%s{\\p0}",
      s.bord, s.color, alpha, s.color, alpha, s.path
    )
  end
  return table.concat(parts, "\n")
end

return logo
