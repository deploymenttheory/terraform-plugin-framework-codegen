resource "thousandeyes_tests_dns_trace" "example" {
  domain   = "example.com"
  interval = 3600
  agents   = [{ agent_id = "3" }]
}
