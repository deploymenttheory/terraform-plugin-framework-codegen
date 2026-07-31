resource "thousandeyes_tests_ftp_server" "example" {
  request_type = "download"
  url          = "tfacc-url"
  username     = "tfacc-username"
  agents       = [{ agent_id = "3" }]
}
