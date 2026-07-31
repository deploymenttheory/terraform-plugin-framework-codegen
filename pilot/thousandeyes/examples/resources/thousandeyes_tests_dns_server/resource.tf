resource "thousandeyes_tests_dns_server" "example" {
  dns_servers = ["tfacc-dns-servers-element"]
  domain      = "tfacc-domain"
  agents      = [{ agent_id = "3" }]
}
