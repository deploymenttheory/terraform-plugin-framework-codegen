# tflint configuration for this repository.
#
# The only Terraform here is provider *usage examples* under pilot/*/examples. There
# are no reusable modules, and that distinction is what the one disabled rule below
# turns on. Trivy and tflint both walk the tree rather than reading super-linter's
# changed-file list, so FILTER_REGEX_EXCLUDE's `^pilot/.*` does not reach them --
# the same reason openapi is handled by trivy's scan.skip-dirs.

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
  # `terraform_required_version` is disabled below for a narrower reason.
  enabled = false
}

rule "terraform_required_version" {
  # The examples state a minimum Terraform version by convention, and that is
  # real, checkable information. But tflint walks the whole tree, and the tree
  # also holds generated acceptance *fixtures* under testdata/ -- fragments by
  # design, with no terraform block at all, because the acceptance harness
  # supplies the provider through ProtoV6ProviderFactories and gates Terraform
  # versions with tfversion checks in the test itself. A version stanza in a
  # fixture would be dead text the harness ignores, and the rule cannot be
  # scoped per path, so it goes.
  enabled = false
}

rule "terraform_unused_declarations" {
  # The datasource acceptance fixtures declare a data block nothing else in the
  # file references -- by design. The generated test applies the fixture and
  # asserts against Terraform *state*, so the data block's job is to exist and
  # be readable, not to feed another expression. The rule reads that as unused;
  # per-path scoping is not available, so it goes the way of the other two.
  enabled = false
}
