terraform {
  required_providers {
    thousandeyes = {
      source = "deploymenttheory/thousandeyes"
    }
  }
}

provider "thousandeyes" {
  # bearer_token is read from THOUSANDEYES_BEARER_TOKEN when it is not set here.
}

# Only the required attributes.
resource "thousandeyes_tag" "minimal" {
  key         = "environment"
  object_type = "test"
}

# Every attribute the provider exposes, including both nested collections.
resource "thousandeyes_tag" "maximal" {
  key         = "environment"
  value       = "production"
  object_type = "test"

  description = "Applied to every production test."
  color       = "#8A2BE2"
  icon        = "ICON_STAR"
  access_type = "all"
  match_type  = "all"

  assignments = [
    {
      id   = "12345"
      type = "test"
    },
    {
      id   = "67890"
      type = "dashboard"
    },
  ]

  filters = [
    {
      key    = "hostname"
      mode   = "include"
      scope  = "endpoint-agent"
      values = ["prod-web-01", "prod-web-02"]
    },
  ]
}
