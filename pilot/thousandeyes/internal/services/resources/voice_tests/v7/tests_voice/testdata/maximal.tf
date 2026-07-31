# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_voice" "test" {
  # Where a value came from.
  #
  # dscp_id: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled        = true
  bgp_measurements      = true
  codec_id              = "tfacc-codec-id"
  description           = "tfacc-description"
  dscp_id               = "0"
  duration              = 5
  enabled               = true
  interval              = 1
  jitter_buffer         = 1
  num_path_traces       = 1
  port                  = 1024
  randomized_start_time = true
  target_agent_id       = "tfacc-target-agent-id"
  test_name             = "tfacc-test-name"
  use_public_bgp        = true
  agents                = [{ agent_id = "3" }]
}
