# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_agent_to_server" "test" {
  # Where a value came from.
  #
  # dscp_id: documented; unprobed
  #
  # ipv6_policy: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled         = true
  bandwidth_measurements = true
  bgp_measurements       = true
  continuous_mode        = true
  description            = "tfacc-description"
  dscp_id                = "0"
  enabled                = true
  fixed_packet_rate      = 1
  interval               = 1
  ipv6_policy            = "force-ipv4"
  mtu_measurements       = true
  network_measurements   = true
  num_path_traces        = 1
  path_trace_mode        = "classic"
  ping_payload_size      = 1
  port                   = 1
  probe_mode             = "auto"
  protocol               = "tcp"
  randomized_start_time  = true
  server                 = "tfacc-server"
  test_name              = "tfacc-test-name"
  use_public_bgp         = true
  agents                 = [{ agent_id = "3" }]
}
