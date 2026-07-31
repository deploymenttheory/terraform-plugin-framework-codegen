# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_dns_server" "test" {
  # Where a value came from.
  #
  # dns_query_class: documented; unprobed
  #
  # dns_transport_protocol: documented; unprobed
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
  description            = "tfacc-description"
  dns_query_class        = "in"
  dns_servers            = ["tfacc-dns-servers-element"]
  dns_transport_protocol = "udp"
  domain                 = "tfacc-domain"
  enabled                = true
  fixed_packet_rate      = 1
  interval               = 1
  ipv6_policy            = "force-ipv4"
  mtu_measurements       = true
  network_measurements   = true
  num_path_traces        = 1
  path_trace_mode        = "classic"
  probe_mode             = "auto"
  protocol               = "tcp"
  randomized_start_time  = true
  recursive_queries      = true
  test_name              = "tfacc-test-name"
  use_public_bgp         = true
  agents                 = [{ agent_id = "3" }]
}
