# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_bgp" "test" {
  # Where a value came from.
  #
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # include_covered_prefixes: the API's own default, so a value it is known to accept
  #
  # prefix: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # use_public_bgp: the API's own default, so a value it is known to accept
  alerts_enabled           = true
  description              = "tfacc-description"
  enabled                  = true
  include_covered_prefixes = false
  prefix                   = "8.8.8.0/24"
  test_name                = "tfacc-test-name"
  use_public_bgp           = true
}
