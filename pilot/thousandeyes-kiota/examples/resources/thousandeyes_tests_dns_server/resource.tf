resource "thousandeyes_tests_dns_server" "example" {
  dns_servers = ["8.8.8.8"]
  domain      = "example.com"
  interval    = 3600
  agents      = [{ agent_id = "3" }]
}
