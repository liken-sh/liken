-- The header, the passive top-left region. It draws the current item's name
-- from the resolved presentation fields, and takes no focus. A movie shows
-- its title. A series shows the series name over a season and episode line.
local theme = require("theme")
local presentation = require("presentation")

local header = {}

local LEFT = theme.margin.x
local TOP_Y = 90
-- The second line sits below the title, far enough to clear the title glyphs.
local SECOND_Y = TOP_Y + 82

-- A season or an episode arrives as a JSON number. Render it as a whole
-- number, so the line reads "Season 2", not "Season 2.0".
local function num(v)
  if type(v) == "number" then
    return string.format("%d", v)
  end
  return tostring(v)
end

-- The month names, indexed one to twelve, so a date reads with its month
-- spelled out.
local MONTHS = {
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
}

-- format_date turns an ISO date, YYYY-MM-DD, into a readable form like
-- "March 5, 2017". A string that is not an ISO date returns unchanged, so a
-- library that hands a ready-made date shows it as it is.
local function format_date(date)
  local y, m, d = date:match("^(%d%d%d%d)-(%d%d)-(%d%d)$")
  if y then
    local month = MONTHS[tonumber(m)]
    if month then
      return month .. " " .. tonumber(d) .. ", " .. y
    end
  end
  return date
end

-- The second line joins the season, the episode number, and the episode
-- title, and shows only the parts the item declared.
local function second_line()
  local segs = {}
  local season = presentation.season()
  local episode = presentation.episode()
  local etitle = presentation.episode_title()
  local date = presentation.date()
  if season ~= nil then
    segs[#segs + 1] = "Season " .. num(season)
  end
  if episode ~= nil then
    segs[#segs + 1] = "Episode " .. num(episode)
  end
  if etitle and etitle ~= "" then
    segs[#segs + 1] = etitle
  end
  if date and date ~= "" then
    segs[#segs + 1] = format_date(date)
  end
  if #segs == 0 then
    return nil
  end
  return table.concat(segs, "  \194\183  ")
end

-- A series shows the series name over the season line. Everything else with a
-- title shows the title. A field that resolved to nothing draws nothing.
function header.draw()
  local parts = {}
  if presentation.hint() == "series" then
    local series = presentation.series() or presentation.title()
    if series then
      parts[#parts + 1] = theme.text(LEFT, TOP_Y, series, theme.type.title, theme.color.text, 7)
    end
    local line = second_line()
    if line then
      parts[#parts + 1] = theme.text(LEFT, SECOND_Y, line, theme.type.small, theme.color.muted, 7)
    end
  else
    local title = presentation.title()
    if title then
      parts[#parts + 1] = theme.text(LEFT, TOP_Y, title, theme.type.title, theme.color.text, 7)
      local year = presentation.year()
      if year ~= nil then
        parts[#parts + 1] = theme.text(LEFT, SECOND_Y, num(year), theme.type.small, theme.color.muted, 7)
      end
    end
  end
  if #parts == 0 then
    return nil
  end
  return table.concat(parts, "\n")
end

return header
