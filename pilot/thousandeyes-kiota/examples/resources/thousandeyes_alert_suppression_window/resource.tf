resource "thousandeyes_alert_suppression_window" "example" {
  duration   = 3600
  name       = "tfacc-name"
  repeat     = { type = "none" }
  start_date = "2027-06-01T00:00:00Z"
}
