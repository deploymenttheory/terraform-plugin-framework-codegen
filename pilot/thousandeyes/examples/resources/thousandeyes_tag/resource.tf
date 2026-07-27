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

resource "thousandeyes_tag" "example" {
  key         = "environment"
  value       = "production"
  object_type = "test"

  description = "Applied to every production test."
  color       = "#8A2BE2"
}
