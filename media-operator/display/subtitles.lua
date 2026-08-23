-- The subtitle control. Its cell shows the current subtitle language, or
-- off when subtitles are disabled. Its chooser lists the subtitle tracks
-- and an off entry, and select switches sid or disables subtitles.
local theme = require("theme")
local chooser = require("chooser")

local subtitles = {}

local redraw_cb = function() end
function subtitles.set_redraw(fn)
  redraw_cb = fn
end

local open = false
local sel = 1

local function tracks()
  local out = {}
  for _, t in ipairs(mp.get_property_native("track-list") or {}) do
    if t.type == "sub" then
      out[#out + 1] = t
    end
  end
  return out
end

function subtitles.available()
  return #tracks() >= 1
end

local function label_for(t)
  local lang = t.lang and t.lang:upper() or nil
  if t.title and t.title ~= "" then
    if lang then
      return t.title .. " (" .. lang .. ")"
    end
    return t.title
  end
  if lang then
    return lang
  end
  return "Track " .. tostring(t.id)
end

local function selected(ts)
  for _, t in ipairs(ts) do
    if t.selected then
      return t
    end
  end
  return nil
end

-- The focused cell draws full brightness with a green caption. The other
-- cells draw subdued, so brightness alone marks which control has focus.
function subtitles.draw(x, y, focused)
  local cur = selected(tracks())
  local status = cur and (cur.lang and cur.lang:upper() or label_for(cur)) or "off"
  local alpha = focused and theme.alpha.opaque or theme.alpha.subdued
  local caption = focused and theme.color.fill or theme.color.muted
  local parts = {}
  parts[#parts + 1] = theme.text(x, y, "SUBS", theme.type.small, caption, 7, alpha)
  parts[#parts + 1] = theme.text(x, y + 40, status, theme.type.label, theme.color.text, 7, alpha)
  return table.concat(parts, "\n")
end

-- Off is the first entry, ahead of the tracks, so a viewer can always turn
-- subtitles off. Entry 1 maps to sid no, and every later entry maps to the
-- track one place before it.
local function entries()
  local list = { "Off" }
  for _, t in ipairs(tracks()) do
    list[#list + 1] = label_for(t)
  end
  return list
end

function subtitles.activate()
  local ts = tracks()
  sel = 1
  for i, t in ipairs(ts) do
    if t.selected then
      sel = i + 1
    end
  end
  open = true
  return subtitles
end

function subtitles.is_open()
  return open
end

function subtitles.close()
  open = false
end

-- The chooser is a vertical list, so up moves to the previous entry and down
-- to the next. select applies the picked track or off and closes.
function subtitles.handle(action)
  local n = #entries()
  if action == "up" then
    sel = math.max(1, sel - 1)
  elseif action == "down" then
    sel = math.min(n, sel + 1)
  elseif action == "select" then
    if sel == 1 then
      mp.set_property("sid", "no")
    else
      local t = tracks()[sel - 1]
      if t then
        mp.set_property("sid", tostring(t.id))
      end
    end
    open = false
  end
end

function subtitles.draw_chooser()
  return chooser.draw("Subtitles", entries(), sel)
end

return subtitles
