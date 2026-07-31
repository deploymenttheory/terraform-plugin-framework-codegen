ephemeral "thousandeyes_credential" "example" {
  id = "<id>"
}

# An ephemeral value lives for the run and never reaches state. Reference it from
# another provider's configuration, e.g.:
#
#   ephemeral.thousandeyes_credential.example.value
