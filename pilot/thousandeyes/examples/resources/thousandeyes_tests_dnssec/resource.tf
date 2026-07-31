resource "thousandeyes_tests_dnssec" "example" {
  domain = "tfacc-domain"
  agents = [{ agent_id = "3" }]
}
