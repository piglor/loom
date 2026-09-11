# Backup verification can read only the non-sensitive restore canary. It must
# never be able to read or mutate tenant/plugin credentials.
path "loom/data/restore-check" {
  capabilities = ["read"]
}
