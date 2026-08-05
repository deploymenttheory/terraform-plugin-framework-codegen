resource "thousandeyes_tests_web_transaction" "example" {
  interval           = 3600
  transaction_script = "tfacc-transaction-script"
  url                = "https://www.example.com"
  agents             = [{ agent_id = "3" }]
}
