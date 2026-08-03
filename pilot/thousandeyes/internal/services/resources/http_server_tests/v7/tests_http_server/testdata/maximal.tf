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
  # auth_type: documented; unprobed
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
  # request_method: documented; unprobed
  #
  # ssl_version_id: documented; unprobed
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
  bandwidth_measurements            = true
  bgp_measurements                  = true
  client_certificate                = "tfacc-client-certificate"
  collect_proxy_network_data        = true
  content_regex                     = "tfacc-content-regex"
  description                       = "tfacc-description"
  desired_status_code               = "tfacc-desired-status-code"
  distributed_tracing               = true
  dns_override                      = "tfacc-dns-override"
  download_limit                    = 1
  enabled                           = true
  fixed_packet_rate                 = 1
  follow_redirects                  = true
  headers                           = ["tfacc-headers-element"]
  http_target_time                  = 100
  http_time_limit                   = 5
  http_version                      = 1
  include_headers                   = true
  interval                          = "60"
  ipv6_policy                       = "force-ipv4"
  mtu_measurements                  = true
  network_measurements              = true
  num_path_traces                   = 1
  override_agent_proxy              = true
  override_proxy_id                 = "tfacc-override-proxy-id"
  path_trace_mode                   = "classic"
  post_body                         = "tfacc-post-body"
  probe_mode                        = "auto"
  protocol                          = "tcp"
  randomized_start_time             = true
  request_method                    = "get"
  ssl_version_id                    = "0"
  test_name                         = "tfacc-test-name"
  url                               = "tfacc-url"
  use_ntlm                          = true
  use_public_bgp                    = true
  user_agent                        = "tfacc-user-agent"
  username                          = "tfacc-username"
  verify_certificate                = true
  agents                            = [{ agent_id = "3" }]
}
