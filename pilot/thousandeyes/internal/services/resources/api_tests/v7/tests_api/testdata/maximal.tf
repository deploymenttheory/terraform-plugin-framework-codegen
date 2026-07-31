# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tests_api" "test" {
  # Where a value came from.
  #
  # path_trace_mode: documented; unprobed
  #
  # probe_mode: documented; unprobed
  #
  # protocol: documented; unprobed
  #
  # requests: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # ssl_version_id: documented; unprobed
  #
  # agents: curated in the blueprint's accFixture; the generator cannot derive it
  #
  # Not filled in, because no correct value could be derived. Each of these is optional, so this
  # configuration is valid as it stands -- but it is testing less than it could.
  #
  # predefined_variables: a nested object's members may be identifiers of objects that must
  # already exist, and nothing in the specification says whether they are
  #
  # vault_credentials: a nested object's members may be identifiers of objects that must already
  # exist, and nothing in the specification says whether they are
  alerts_enabled                 = true
  bgp_measurements               = true
  client_cert_domains_allow_list = "tfacc-client-cert-domains-allow-list"
  client_certificate             = "tfacc-client-certificate"
  collect_proxy_network_data     = true
  credentials                    = ["tfacc-credentials-element"]
  description                    = "tfacc-description"
  distributed_tracing            = true
  enabled                        = true
  follow_redirects               = true
  interval                       = 1
  mtu_measurements               = true
  network_measurements           = true
  num_path_traces                = 1
  override_agent_proxy           = true
  override_proxy_id              = "tfacc-override-proxy-id"
  path_trace_mode                = "classic"
  probe_mode                     = "auto"
  protocol                       = "tcp"
  randomized_start_time          = true
  requests                       = [{ name = "step-1", url = "https://api.stripe.com/healthcheck", method = "get" }]
  ssl_version_id                 = "0"
  target_time                    = 1
  test_name                      = "tfacc-test-name"
  time_limit                     = 5
  url                            = "tfacc-url"
  use_public_bgp                 = true
  agents                         = [{ agent_id = "3" }]
}
