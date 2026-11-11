# The organization's repositories, as infrastructure. Each repository's
# public face carries settings that this file declares in one place: the
# description, the website link, the topic tags, and the Pages site that
# serves its manual. A new repository is one entry in the map below.
#
# The resources create a repository that is new to the map, and nothing
# here deletes one. The repositories that predate this file were
# adopted into the state once, so an entry and its real repository
# always match by name.
# The zone's records for the sites are in terraform.tf, so the DNS
# half and the Pages half of every site read from the same directory.
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
    log = {
      description = "The liken devlog"
      cname       = "log.liken.sh"
      topics      = ["liken", "devlog"]
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
    plugins = {
      description = "The catalog of liken's Agent Skills for Claude Code"
      cname       = null
      homepage    = "https://liken.sh"
      topics      = []
    }
    # A fork keeps its issues on the upstream project, so this one has
    # none of its own. Its merges keep GitHub's defaults.
    corrosion = {
      description    = "liken's shallow fork of Corrosion: the ghcr image and a short stack of upstream-bound changes"
      cname          = null
      homepage       = "https://superfly.github.io/corrosion/"
      topics         = []
      has_issues     = false
      merge_defaults = true
    }
  }
}

locals {
  merge_defaults = {
    for name, repo in local.repositories :
    name => lookup(repo, "merge_defaults", false)
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
  has_issues                  = lookup(each.value, "has_issues", true)
  has_projects                = false
  has_wiki                    = false
  has_discussions             = false
  allow_merge_commit          = false
  allow_squash_merge          = true
  allow_rebase_merge          = true
  allow_auto_merge            = true
  delete_branch_on_merge      = true
  web_commit_signoff_required = false

  # A squash merge takes the pull request's title and body as its
  # commit message, and a pull request offers to update its branch
  # from the base. merge_defaults keeps GitHub's defaults on a
  # repository that asks for them.
  allow_update_branch         = !local.merge_defaults[each.key]
  squash_merge_commit_title   = local.merge_defaults[each.key] ? "COMMIT_OR_PR_TITLE" : "PR_TITLE"
  squash_merge_commit_message = local.merge_defaults[each.key] ? "COMMIT_MESSAGES" : "PR_BODY"

  lifecycle {
    # Terraform must never delete a repository because an entry left
    # the map.
    prevent_destroy = true
    # Dependabot alert state is per-repository history, not public
    # face, so this file does not manage it.
    ignore_changes = [vulnerability_alerts]
  }
}

# Each site is one Pages configuration: a workflow deploy (a publish
# job in every repository's CI workflow, so no branch source), the
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
