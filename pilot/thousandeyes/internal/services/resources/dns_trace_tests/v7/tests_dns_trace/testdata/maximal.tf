# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_dns_trace" "test" {
  # Where a value came from.
  #
  # dns_query_class: documented; unprobed
  #
  # dns_transport_protocol: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled         = true
  description            = "tfacc-description"
  dns_query_class        = "in"
  dns_transport_protocol = "udp"
  domain                 = "tfacc-domain"
  enabled                = true
  interval               = 1
  randomized_start_time  = true
  test_name              = "tfacc-test-name"
  agents                 = [{ agent_id = "3" }]
}
