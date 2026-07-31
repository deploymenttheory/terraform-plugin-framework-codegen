# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_agent_to_agent" "test" {
  # Where a value came from.
  #
  # direction: documented; unprobed
  #
  # dscp_id: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # protocol: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled          = true
  bgp_measurements        = true
  description             = "tfacc-description"
  direction               = "to-target"
  dscp_id                 = "0"
  enabled                 = true
  fixed_packet_rate       = 1
  interval                = 1
  mss                     = 20
  num_path_traces         = 1
  path_trace_mode         = "classic"
  port                    = 1
  protocol                = "tcp"
  randomized_start_time   = true
  target_agent_id         = "tfacc-target-agent-id"
  test_name               = "tfacc-test-name"
  throughput_duration     = 5000
  throughput_measurements = true
  throughput_rate         = 8
  use_public_bgp          = true
  agents                  = [{ agent_id = "3" }]
}
