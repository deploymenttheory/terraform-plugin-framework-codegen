resource "thousandeyes_alerts_rule" "example" {
  alert_type              = "http-server"
  expression              = "((responseTime >= 1000 ms))"
  rounds_violating_out_of = 2
  rule_name               = "tfacc-rule-name"
}
