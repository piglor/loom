# The application can create, rotate, read and soft-delete its own values.
# Permanent metadata destruction is an operator-only action.
path "loom/data/organizations/*" {
  capabilities = ["create", "read", "update", "delete"]
}
