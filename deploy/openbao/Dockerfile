FROM openbao/openbao:2.5.4@sha256:d0424c95859f7b4c1e308abf57c4cd72b9cba835bb946eb397172b799fba9477

# The official image drops from root to the openbao user at startup. Seed the
# data directory with the same ownership so a newly-created Docker volume keeps
# the least-privilege runtime user instead of becoming root-owned.
COPY --chown=openbao:openbao deploy/openbao/config/openbao.hcl /openbao/config/openbao.hcl
COPY --chown=openbao:openbao deploy/openbao/config/loom-policy.hcl /openbao/config/loom-policy.hcl
RUN mkdir -p /openbao/data /openbao/logs \
    && chown -R openbao:openbao /openbao/data /openbao/logs /openbao/config
