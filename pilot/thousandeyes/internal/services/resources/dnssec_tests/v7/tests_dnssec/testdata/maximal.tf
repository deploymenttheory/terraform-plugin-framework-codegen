# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_dnssec" "test" {
  # Where a value came from.
  #
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # dns_query_class: documented; unprobed
  #
  # domain: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # interval: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # randomized_start_time: the API's own default, so a value it is known to accept
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled        = true
  description           = "tfacc-description"
  dns_query_class       = "in"
  domain                = "cloudflare.com"
  enabled               = true
  interval              = 3600
  randomized_start_time = false
  test_name             = "tfacc-test-name"
  agents                = [{ agent_id = "3" }]
}
