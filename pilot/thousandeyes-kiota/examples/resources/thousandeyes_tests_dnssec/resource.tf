resource "thousandeyes_tests_dnssec" "example" {
  domain   = "cloudflare.com"
  interval = 3600
  agents   = [{ agent_id = "3" }]
}
