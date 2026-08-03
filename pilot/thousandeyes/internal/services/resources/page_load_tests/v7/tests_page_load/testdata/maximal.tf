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
  # alerts_enabled: the API's own default, so a value it is known to accept
  #
  # allow_geolocation: the API's own default, so a value it is known to accept
  #
  # allow_mic_and_camera: the API's own default, so a value it is known to accept
  #
  # allow_unsafe_legacy_renegotiation: the API's own default, so a value it is known to accept
  #
  # auth_type: documented; unprobed
  #
  # bandwidth_measurements: the API's own default, so a value it is known to accept
  #
  # bgp_measurements: the API's own default, so a value it is known to accept
  #
  # block_domains: the API's own default, so a value it is known to accept
  #
  # browser_language: the API's own default, so a value it is known to accept
  #
  # content_regex: the API's own default, so a value it is known to accept
  #
  # disable_screenshot: the API's own default, so a value it is known to accept
  #
  # distributed_tracing: the API's own default, so a value it is known to accept
  #
  # emulated_device_id: the API's own default, so a value it is known to accept
  #
  # enabled: the API's own default, so a value it is known to accept
  #
  # follow_redirects: the API's own default, so a value it is known to accept
  #
  # http_interval: documented; unprobed
  #
  # http_target_time: the API's own default, so a value it is known to accept
  #
  # http_time_limit: the API's own default, so a value it is known to accept
  #
  # http_version: the API's own default, so a value it is known to accept
  #
  # identify_agent_traffic_with_user_agent: the API's own default, so a value it is known to
  # accept
  #
  # include_headers: the API's own default, so a value it is known to accept
  #
  # interval: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # mtu_measurements: the API's own default, so a value it is known to accept
  #
  # network_measurements: the API's own default, so a value it is known to accept
  #
  # num_path_traces: the API's own default, so a value it is known to accept
  #
  # override_agent_proxy: the API's own default, so a value it is known to accept
  #
  # page_load_target_time: the API's own default, so a value it is known to accept
  #
  # page_load_time_limit: the API's own default, so a value it is known to accept
  #
  # page_loading_strategy: documented; unprobed
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: observed accepted; the API refused udp
  #
  # randomized_start_time: the API's own default, so a value it is known to accept
  #
  # ssl_version_id: documented; unprobed
  #
  # subinterval: documented; unprobed
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
  alerts_enabled                         = true
  allow_geolocation                      = false
  allow_mic_and_camera                   = false
  allow_unsafe_legacy_renegotiation      = true
  auth_type                              = "none"
  bandwidth_measurements                 = false
  bgp_measurements                       = true
  block_domains                          = ""
  browser_language                       = "en-US"
  chrome_options                         = "tfacc-chrome-options"
  chrome_policies                        = "tfacc-chrome-policies"
  client_certificate                     = "tfacc-client-certificate"
  collect_proxy_network_data             = true
  content_regex                          = ""
  description                            = "tfacc-description"
  desired_status_code                    = "tfacc-desired-status-code"
  disable_screenshot                     = false
  distributed_tracing                    = false
  dns_override                           = "tfacc-dns-override"
  download_limit                         = 1
  emulated_device_id                     = "281474976710656"
  enabled                                = true
  fixed_packet_rate                      = 1
  follow_redirects                       = true
  http_interval                          = "60"
  http_target_time                       = 1000
  http_time_limit                        = 5
  http_version                           = 2
  identify_agent_traffic_with_user_agent = false
  include_headers                        = true
  interval                               = 3600
  mtu_measurements                       = true
  network_measurements                   = true
  num_path_traces                        = 3
  override_agent_proxy                   = false
  override_proxy_id                      = "tfacc-override-proxy-id"
  page_load_target_time                  = 6
  page_load_time_limit                   = 10
  page_loading_strategy                  = "normal"
  path_trace_mode                        = "classic"
  probe_mode                             = "auto"
  protocol                               = "tcp"
  randomized_start_time                  = false
  ssl_version_id                         = "0"
  subinterval                            = "60"
  test_name                              = "tfacc-test-name"
  url                                    = "https://www.example.com"
  use_ntlm                               = false
  use_public_bgp                         = true
  user_agent                             = "tfacc-user-agent"
  username                               = "tfacc-username"
  verify_certificate                     = true
  agents                                 = [{ agent_id = "3" }]
}
