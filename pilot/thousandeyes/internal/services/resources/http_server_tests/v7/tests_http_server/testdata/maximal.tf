# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_http_server" "test" {
  # Where a value came from.
  #
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # allow_unsafe_legacy_renegotiation: the API's own default, so a value it is known to accept
  #
  # auth_type: documented; unprobed
  #
  # bandwidth_measurements: the API's own default, so a value it is known to accept
  #
  # bgp_measurements: the API's own default, so a value it is known to accept
  #
  # collect_proxy_network_data: the API's own default, so a value it is known to accept
  #
  # content_regex: the API's own default, so a value it is known to accept
  #
  # distributed_tracing: the API's own default, so a value it is known to accept
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # follow_redirects: the API's own default, so a value it is known to accept
  #
  # http_target_time: the API's own default, so a value it is known to accept
  #
  # http_time_limit: the API's own default, so a value it is known to accept
  #
  # http_version: the API's own default, so a value it is known to accept
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
  # override_agent_proxy: the API's own default, so a value it is known to accept
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: observed accepted; the API refused udp
  #
  # randomized_start_time: the API's own default, so a value it is known to accept
  #
  # request_method: documented; unprobed
  #
  # ssl_version_id: documented; unprobed
  #
  # url: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # use_ntlm: the API's own default, so a value it is known to accept
  #
  # use_public_bgp: the API's own default, so a value it is known to accept
  #
  # verify_certificate: the API's own default, so a value it is known to accept
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # Not filled in, because no correct value could be derived. Each of these is optional, so this
  # configuration is valid as it stands -- but it is testing less than it could.
  #
  # password: it is credential-shaped, and a generated fixture must not invent values that read
  # as secrets; supply one through accFixture if the test needs it
  #
  # vault_credentials: a nested object's members may be identifiers of objects that must already
  # exist, and nothing in the specification says whether they are
  alerts_enabled                    = true
  allow_unsafe_legacy_renegotiation = true
  auth_type                         = "none"
  bandwidth_measurements            = false
  bgp_measurements                  = true
  client_certificate                = "tfacc-client-certificate"
  collect_proxy_network_data        = false
  content_regex                     = ""
  description                       = "tfacc-description"
  desired_status_code               = "tfacc-desired-status-code"
  distributed_tracing               = false
  dns_override                      = "tfacc-dns-override"
  download_limit                    = 1
  enabled                           = true
  fixed_packet_rate                 = 1
  follow_redirects                  = true
  headers                           = ["tfacc-headers-element"]
  http_target_time                  = 1000
  http_time_limit                   = 5
  http_version                      = 2
  include_headers                   = true
  interval                          = 3600
  ipv6_policy                       = "force-ipv4"
  mtu_measurements                  = true
  network_measurements              = true
  num_path_traces                   = 3
  override_agent_proxy              = false
  override_proxy_id                 = "tfacc-override-proxy-id"
  path_trace_mode                   = "classic"
  post_body                         = "tfacc-post-body"
  probe_mode                        = "auto"
  protocol                          = "tcp"
  randomized_start_time             = false
  request_method                    = "get"
  ssl_version_id                    = "0"
  test_name                         = "tfacc-test-name"
  url                               = "https://www.example.com"
  use_ntlm                          = false
  use_public_bgp                    = true
  user_agent                        = "tfacc-user-agent"
  username                          = "tfacc-username"
  verify_certificate                = true
  agents                            = [{ agent_id = "3" }]
}
