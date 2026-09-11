path "loom/data/organizations/*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "loom/metadata/organizations/*" {
  capabilities = ["read", "list", "delete"]
}
