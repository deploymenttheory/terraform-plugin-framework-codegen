# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_page_load" "test" {
  # Where a value came from.
  #
  # auth_type: documented; unprobed
  #
  # http_interval: documented; unprobed
  #
  # interval: documented; unprobed
  #
  # page_loading_strategy: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: documented; unprobed
  #
  # ssl_version_id: documented; unprobed
  #
  # subinterval: documented; unprobed
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
  alerts_enabled                         = true
  allow_geolocation                      = true
  allow_mic_and_camera                   = true
  allow_unsafe_legacy_renegotiation      = true
  auth_type                              = "none"
  bandwidth_measurements                 = true
  bgp_measurements                       = true
  block_domains                          = "tfacc-block-domains"
  browser_language                       = "tfacc-browser-language"
  chrome_options                         = "tfacc-chrome-options"
  chrome_policies                        = "tfacc-chrome-policies"
  client_certificate                     = "tfacc-client-certificate"
  collect_proxy_network_data             = true
  content_regex                          = "tfacc-content-regex"
  description                            = "tfacc-description"
  desired_status_code                    = "tfacc-desired-status-code"
  disable_screenshot                     = true
  distributed_tracing                    = true
  dns_override                           = "tfacc-dns-override"
  download_limit                         = 1
  emulated_device_id                     = "tfacc-emulated-device-id"
  enabled                                = true
  fixed_packet_rate                      = 1
  follow_redirects                       = true
  http_interval                          = "60"
  http_target_time                       = 100
  http_time_limit                        = 5
  http_version                           = 1
  identify_agent_traffic_with_user_agent = true
  include_headers                        = true
  interval                               = "60"
  mtu_measurements                       = true
  network_measurements                   = true
  num_path_traces                        = 1
  override_agent_proxy                   = true
  override_proxy_id                      = "tfacc-override-proxy-id"
  page_load_target_time                  = 1
  page_load_time_limit                   = 5
  page_loading_strategy                  = "normal"
  path_trace_mode                        = "classic"
  probe_mode                             = "auto"
  protocol                               = "tcp"
  randomized_start_time                  = true
  ssl_version_id                         = "0"
  subinterval                            = "60"
  test_name                              = "tfacc-test-name"
  url                                    = "tfacc-url"
  use_ntlm                               = true
  use_public_bgp                         = true
  user_agent                             = "tfacc-user-agent"
  username                               = "tfacc-username"
  verify_certificate                     = true
  agents                                 = [{ agent_id = "3" }]
}
