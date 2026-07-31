resource "thousandeyes_tests_dns_trace" "example" {
  domain = "tfacc-domain"
  agents = [{ agent_id = "3" }]
}
