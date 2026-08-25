# The organization's repositories, as infrastructure. Each repository's
# public face carries settings that this file declares in one place: the
# description, the website link, the topic tags, and the Pages site that
# serves its manual. Before this file, each was set by hand in eight
# separate settings pages, and a new repository started from none of
# them. Now a new repository is one entry in the map below.
#
# The resources adopt the existing repositories through the import
# blocks at the bottom; nothing here creates or deletes a repository.
# The zone's records for the sites are above in terraform.tf, in the
# extension_operators set, so the DNS half and the Pages half of every
# site read from the same directory.
#
# One thing stays outside this file on purpose. GitHub issues and
# renews each Pages certificate on its own, so there is nothing to
# declare for TLS. The redirect from http is declared here, on each
# site's pages resource, as https_enforced.

locals {
  # Every repository the organization has. A cname makes a Pages site:
  # the resource below builds the pages block and the homepage link
  # from it. A repository with no site states its homepage directly, or
  # none at all.
  repositories = {
    liken = {
      description = "Linux + Kubernetes"
      cname       = "liken.sh"
      topics      = ["liken", "kubernetes", "k3s", "linux", "linux-distribution", "dynamic-resource-allocation"]
    }
    audio-operator = {
      description = "Publishes audio outputs as DRA devices on liken clusters"
      cname       = "audio.liken.sh"
      topics      = ["liken", "kubernetes", "kubernetes-operator", "dynamic-resource-allocation", "audio", "pipewire"]
    }
    display-operator = {
      description = "Publishes monitor outputs as DRA devices on liken clusters"
      cname       = "display.liken.sh"
      topics      = ["liken", "kubernetes", "kubernetes-operator", "dynamic-resource-allocation", "drm", "ddc"]
    }
    bluetooth-operator = {
      description = "Publishes paired Bluetooth controllers as DRA devices on liken clusters"
      cname       = "bluetooth.liken.sh"
      topics      = ["liken", "kubernetes", "kubernetes-operator", "dynamic-resource-allocation", "bluetooth", "bluez"]
    }
    media-operator = {
      description = "Routing and control of media playback on liken clusters"
      cname       = "media.liken.sh"
      topics      = ["liken", "kubernetes", "kubernetes-operator", "dynamic-resource-allocation", "mqtt", "mpv", "media"]
    }
    log = {
      description = "The liken devlog"
      cname       = "log.liken.sh"
      topics      = ["liken", "devlog"]
    }
    brand = {
      description = "The liken brand assets"
      cname       = null
      homepage    = "https://liken.sh"
      topics      = ["liken", "hugo-theme"]
    }
    liken-dev-cluster = {
      description = "Dev fleet repository for liken GitOps drills"
      cname       = null
      topics      = ["liken", "gitops", "flux"]
    }
    ".github" = {
      description = "The liken-sh organization profile"
      cname       = null
      topics      = []
    }
  }
}

resource "github_repository" "repositories" {
  for_each = local.repositories

  name        = each.key
  description = each.value.description
  visibility  = "public"

  # A site repository's homepage is its own site. GitHub renders the
  # link at the top of the repository page, beside the description.
  homepage_url = each.value.cname != null ? "https://${each.value.cname}" : lookup(each.value, "homepage", null)

  topics = each.value.topics

  # The settings every repository shares, stated so an apply never
  # writes a provider default over a value someone chose.
  has_issues                  = true
  has_projects                = false
  has_wiki                    = false
  has_discussions             = false
  allow_merge_commit          = false
  allow_squash_merge          = true
  allow_rebase_merge          = true
  allow_auto_merge            = true
  delete_branch_on_merge      = true
  web_commit_signoff_required = false

  lifecycle {
    # Terraform must never delete a repository because an entry left
    # the map.
    prevent_destroy = true
    # Dependabot alert state is per-repository history, not public
    # face, so this file does not manage it.
    ignore_changes = [vulnerability_alerts]
  }
}

# Each site is one Pages configuration: a workflow deploy (the same
# docs.yaml shape in every repository, so no branch source), the
# custom domain whose CNAME is in terraform.tf, and the redirect from
# http. The github_repository resource also offers an inline pages
# block, but the provider deprecates it, and it cannot state
# https_enforced; this resource can.
resource "github_repository_pages" "sites" {
  for_each = { for name, repo in local.repositories : name => repo.cname if repo.cname != null }

  repository     = github_repository.repositories[each.key].name
  build_type     = "workflow"
  cname          = each.value
  https_enforced = true
}

# Adoption. Each existing repository and site maps onto its entry by
# name; the first apply records the identity and changes only what the
# resources state differently.
import {
  for_each = local.repositories
  to       = github_repository.repositories[each.key]
  id       = each.key
}

# The five manual sites predate this file, so their Pages
# configurations import. A site added after adoption (the devlog) is
# created by the resource instead, and an import for it would fail,
# because there is nothing to import yet.
import {
  for_each = { for name, repo in local.repositories : name => repo.cname if repo.cname != null && name != "log" }
  to       = github_repository_pages.sites[each.key]
  id       = each.key
}
