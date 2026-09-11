ui = false

# OpenBao 2.5+ removed mlock support. Swap must be disabled or encrypted on the
# host; this setting is required by the pinned image and does not expose data.
disable_mlock = true
# Coolify keeps this listener on the private Compose network. Public TLS ends
# at the Loom ingress; a separately hosted OpenBao must use verified TLS.
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
