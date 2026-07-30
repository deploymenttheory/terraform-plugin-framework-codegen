# tflint configuration for this repository.
#
# The only Terraform here is provider *usage examples* under pilot/*/examples. There
# are no reusable modules, and that distinction is what the one disabled rule below
# turns on. Trivy and tflint both walk the tree rather than reading super-linter's
# changed-file list, so FILTER_REGEX_EXCLUDE's `^pilot/.*` does not reach them --
# the same reason openapi-specs is handled by trivy's scan.skip-dirs.

config {
  # Nothing here is called as a module, so there are no variables to infer.
  call_module_type = "none"
}

plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

rule "terraform_required_providers" {
  # Disabled, and this is the rare case where the rule is actively wrong rather
  # than merely noisy.
  #
  # The rule wants a version constraint on every provider. This provider is not
  # published to the registry -- publishing it is explicitly out of scope -- so any
  # constraint we wrote would name a version that does not exist, and `terraform
  # init` would fail against it. Worse, it would fight the mechanism that makes
  # these examples testable at all: a filesystem `dev_overrides` block, which
  # ignores version constraints and warns when it finds them.
  #
  # `terraform_required_version` is deliberately left enabled: a minimum Terraform
  # version is real, checkable information, and the examples now state one.
  enabled = false
}
