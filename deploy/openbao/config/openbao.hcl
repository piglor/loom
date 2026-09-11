ui = false

# OpenBao 2.5+ removed mlock support. Swap must be disabled or encrypted on the
# host; this setting is required by the pinned image and does not expose data.
disable_mlock = true
# The local/Lite module keeps this listener on its private Compose network.
# The dedicated Coolify resource terminates verified HTTPS at its ingress.
api_addr = "http://openbao:8200"
cluster_addr = "http://openbao:8201"

storage "raft" {
  path = "/openbao/data"
  node_id = "loom-openbao-1"
}

listener "tcp" {
  address = "0.0.0.0:8200"
  tls_disable = true
}

telemetry {
  disable_hostname = true
}

# OpenBao 2.5 treats API-created audit devices as unsafe by default. Keep the
# audit device declarative so every restart has the same protected destination.
audit "file" "loom" {
  description = "Loom OpenBao audit log"
  options {
    file_path = "/openbao/logs/audit.log"
    mode      = "0600"
  }
}
