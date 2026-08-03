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
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # bandwidth_measurements: the API's own default, so a value it is known to accept
  #
  # bgp_measurements: the API's own default, so a value it is known to accept
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # ftp_target_time: the API's own default, so a value it is known to accept
  #
  # ftp_time_limit: the API's own default, so a value it is known to accept
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
  # password: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: observed accepted; the API refused udp
  #
  # randomized_start_time: the API's own default, so a value it is known to accept
  #
  # request_type: observed accepted
  #
  # url: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # use_active_ftp: the API's own default, so a value it is known to accept
  #
  # use_explicit_ftps: the API's own default, so a value it is known to accept
  #
  # use_public_bgp: the API's own default, so a value it is known to accept
  #
  # username: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  alerts_enabled         = true
  bandwidth_measurements = false
  bgp_measurements       = true
  description            = "tfacc-description"
  download_limit         = 1
  enabled                = true
  fixed_packet_rate      = 1
  ftp_target_time        = 1000
  ftp_time_limit         = 5
  interval               = 3600
  ipv6_policy            = "force-ipv4"
  mtu_measurements       = true
  network_measurements   = true
  num_path_traces        = 3
  password               = "guest"
  path_trace_mode        = "classic"
  probe_mode             = "auto"
  protocol               = "tcp"
  randomized_start_time  = false
  request_type           = "download"
  test_name              = "tfacc-test-name"
  url                    = "ftp://speedtest.tele2.net"
  use_active_ftp         = false
  use_explicit_ftps      = false
  use_public_bgp         = true
  username               = "anonymous"
  agents                 = [{ agent_id = "3" }]
}
