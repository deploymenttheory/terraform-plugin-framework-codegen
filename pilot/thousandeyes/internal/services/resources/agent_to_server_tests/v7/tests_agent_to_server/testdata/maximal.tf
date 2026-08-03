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
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # bandwidth_measurements: curated in the blueprint's accFixture; the generator cannot derive
  # it
  #
  # bgp_measurements: the API's own default, so a value it is known to accept
  #
  # continuous_mode: the API's own default, so a value it is known to accept
  #
  # dscp_id: documented; unprobed
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # fixed_packet_rate: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # interval: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # ipv6_policy: documented; unprobed
  #
  # mtu_measurements: the API's own default, so a value it is known to accept
  #
  # network_measurements: the API's own default, so a value it is known to accept
  #
  # num_path_traces: the API's own default, so a value it is known to accept
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: observed accepted; the API refused udp
  #
  # randomized_start_time: the API's own default, so a value it is known to accept
  #
  # server: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # use_public_bgp: the API's own default, so a value it is known to accept
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled         = true
  bandwidth_measurements = false
  bgp_measurements       = true
  continuous_mode        = false
  description            = "tfacc-description"
  dscp_id                = "0"
  enabled                = true
  fixed_packet_rate      = 50
  interval               = 3600
  ipv6_policy            = "force-ipv4"
  mtu_measurements       = true
  network_measurements   = true
  num_path_traces        = 3
  path_trace_mode        = "classic"
  ping_payload_size      = 1
  port                   = 1
  probe_mode             = "auto"
  protocol               = "tcp"
  randomized_start_time  = false
  server                 = "www.example.com"
  test_name              = "tfacc-test-name"
  use_public_bgp         = true
  agents                 = [{ agent_id = "3" }]
}
