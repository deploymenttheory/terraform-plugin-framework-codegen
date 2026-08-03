# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_ftp_server" "test" {
  # Where a value came from.
  #
  # interval: documented; unprobed
  #
  # ipv6_policy: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: documented; unprobed
  #
  # request_type: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # Not filled in, because no correct value could be derived. Each of these is optional, so this
  # configuration is valid as it stands -- but it is testing less than it could.
  #
  # password: it is credential-shaped, and a generated fixture must not invent values that read
  # as secrets; supply one through accFixture if the test needs it
  alerts_enabled         = true
  bandwidth_measurements = true
  bgp_measurements       = true
  description            = "tfacc-description"
  download_limit         = 1
  enabled                = true
  fixed_packet_rate      = 1
  ftp_target_time        = 1000
  ftp_time_limit         = 10
  interval               = "60"
  ipv6_policy            = "force-ipv4"
  mtu_measurements       = true
  network_measurements   = true
  num_path_traces        = 1
  path_trace_mode        = "classic"
  probe_mode             = "auto"
  protocol               = "tcp"
  randomized_start_time  = true
  request_type           = "download"
  test_name              = "tfacc-test-name"
  url                    = "tfacc-url"
  use_active_ftp         = true
  use_explicit_ftps      = true
  use_public_bgp         = true
  username               = "tfacc-username"
  agents                 = [{ agent_id = "3" }]
}
