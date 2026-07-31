terraform {
  # Protocol 6, which Terraform has spoken since 1.0. The provider also registers
  # list resources and actions; using those needs 1.14 or newer, but nothing in
  # this example does.
  required_version = ">= 1.0"

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
  match_type  = "or"

  # assignments is read-only: the probe watched the API discard it on write, so the
  # schema records it as computed. Assign objects to a tag from the object's side.

  filters = [
    {
      key    = "hostname"
      mode   = "include"
      scope  = "endpoint-agent"
      values = ["prod-web-01", "prod-web-02"]
    },
  ]
}
