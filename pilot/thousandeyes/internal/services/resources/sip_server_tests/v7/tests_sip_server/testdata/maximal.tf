# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_sip_server" "test" {
  # Where a value came from.
  #
  # ipv6_policy: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled        = true
  bgp_measurements      = true
  description           = "tfacc-description"
  enabled               = true
  fixed_packet_rate     = 1
  interval              = 1
  ipv6_policy           = "force-ipv4"
  mtu_measurements      = true
  network_measurements  = true
  num_path_traces       = 1
  options_regex         = "tfacc-options-regex"
  path_trace_mode       = "classic"
  probe_mode            = "auto"
  randomized_start_time = true
  register_enabled      = true
  sip_target_time       = 100
  sip_time_limit        = 5
  test_name             = "tfacc-test-name"
  use_public_bgp        = true
  agents                = [{ agent_id = "3" }]
}
