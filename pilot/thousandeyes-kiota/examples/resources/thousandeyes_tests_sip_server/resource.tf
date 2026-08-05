resource "thousandeyes_tests_sip_server" "example" {
  interval = 3600
  agents   = [{ agent_id = "3" }]
}
