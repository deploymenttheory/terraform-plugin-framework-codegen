resource "thousandeyes_tests_web_transaction" "example" {
  transaction_script = "tfacc-transaction-script"
  agents             = [{ agent_id = "3" }]
}
