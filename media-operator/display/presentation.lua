-- The presentation module holds the current item's declared fields and
-- resolves each one for the header. The command sidecar hands it a block over
-- a script-message whenever the playlist reaches a new item.
local utils = require("mp.utils")

local presentation = {}

local redraw_cb = function() end
function presentation.set_redraw(fn)
  redraw_cb = fn
end

-- The current item's declared fields. An empty table means the item declared
-- nothing, so every field falls through to its next tier.
local block = {}

-- receive takes the block as one JSON string. An empty object, or text that
-- does not parse, means the item declared nothing, so every field falls
-- through to its next tier.
function presentation.receive(text)
  local parsed = nil
  if text and text ~= "" then
    parsed = utils.parse_json(text)
  end
  if type(parsed) == "table" then
    block = parsed
  else
    block = {}
  end
  redraw_cb()
end

-- The title resolves in three tiers: the block's own title, then mpv's
-- media-title read from the file, then nothing. The other fields have no file
-- to fall back to, so they come from the block alone.
function presentation.title()
  if block.title and block.title ~= "" then
    return block.title
  end
  local media = mp.get_property("media-title")
  if media and media ~= "" then
    return media
  end
  return nil
end

function presentation.type()
  return block.type
end

function presentation.hint()
  return block.hint
end

function presentation.series()
  return block.series
end

function presentation.season()
  return block.season
end

function presentation.episode()
  return block.episode
end

function presentation.episode_title()
  return block.episodeTitle
end

-- A standalone track plays as a plain file, so mpv reads its tags, and the
-- artist and the album resolve from the block first and from the tags next.
-- An album plays as one timeline, and mpv reads the first track's tags and
-- keeps them for the whole run. So the block states the album's own words,
-- and the tag tier behind it reads track one.
local function tag(name)
  local value = mp.get_property("metadata/by-key/" .. name)
  if value and value ~= "" then
    return value
  end
  return nil
end

function presentation.artist()
  if block.artist and block.artist ~= "" then
    return block.artist
  end
  return tag("artist")
end

function presentation.album()
  if block.album and block.album ~= "" then
    return block.album
  end
  return tag("album")
end

-- The music year takes the block's own year first, then the leading four
-- digits of the file's date tag, which often carries a whole date. The film
-- year below keeps the block as its only tier, so a film file with a date
-- tag never shows a year its block did not declare.
function presentation.music_year()
  if block.year ~= nil and block.year ~= "" then
    return block.year
  end
  local date = tag("date")
  if date then
    return date:match("^(%d%d%d%d)")
  end
  return nil
end

function presentation.year()
  return block.year
end

function presentation.date()
  return block.date
end

-- The logo is a resolved reference: an in-pod path the bridge reads, or an
-- https URL the bridge fetches. The header asks the bridge to decode it, and
-- the display never opens the file itself.
function presentation.logo()
  if block.logo and block.logo ~= "" then
    return block.logo
  end
  return nil
end

-- The trickplay reference, the sprite-sheet directory the bridge crops tiles
-- from. The display never opens it, and nil means the item shows no thumbnail.
function presentation.trickplay()
  if block.trickplay and block.trickplay ~= "" then
    return block.trickplay
  end
  return nil
end

return presentation
