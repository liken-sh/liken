-- The image stop. A still photo has no timeline, so it shows no scrubber, and
-- left and right step across the photos in the playlist instead of seeking.
local theme = require("theme")
local presentation = require("presentation")

local images = {}

local redraw_cb = function() end
function images.set_redraw(fn)
  redraw_cb = fn
end

-- The counter sits low and centered, on the row the scrubber bar holds for a
-- film.
local function center_x()
  return theme.canvas.w / 2
end
local Y = theme.bar_y

-- An image item is one whose presentation block declares the image type.
function images.available()
  return presentation.type() == "image"
end

-- left and right walk the playlist across the photos. This is the first use
-- of the multi-item list for navigation, not for seeking within one item.
function images.press(action)
  if action == "left" then
    mp.commandv("playlist-prev")
  elseif action == "right" then
    mp.commandv("playlist-next")
  end
  redraw_cb()
end

-- The position counter, the item over the count. playlist-pos counts from
-- zero, so the item is one more.
function images.draw()
  local pos = mp.get_property_number("playlist-pos") or 0
  local count = mp.get_property_number("playlist-count") or 1
  local label = string.format("%d of %d", pos + 1, count)
  return theme.text(center_x(), Y, label, theme.type.small, theme.color.muted, 5)
end

return images
