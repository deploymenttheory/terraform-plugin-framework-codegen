# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_bgp" "test" {
  alerts_enabled           = true
  description              = "tfacc-description"
  enabled                  = true
  include_covered_prefixes = true
  prefix                   = "tfacc-prefix"
  test_name                = "tfacc-test-name"
  use_public_bgp           = true
}
