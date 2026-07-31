terraform {
  required_providers {
    thousandeyes = {
      source = "deploymenttheory/thousandeyes"
    }
  }
}

provider "thousandeyes" {
  # Credentials are read from the environment when not set here.
}
